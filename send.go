package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
)

func runSend(root string, nCh int, codeFile string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !fi.IsDir() && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file or directory", root)
	}
	top := filepath.Base(abs)

	sess, err := newSession()
	if err != nil {
		return err
	}
	pc, err := sess.newPeerConnection()
	if err != nil {
		return err
	}
	defer pc.Close()

	errCh := make(chan error, 1)
	fail := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed {
			fail(errors.New("connection failed"))
		}
	})

	ctrl, err := pc.CreateDataChannel("ctrl", nil)
	if err != nil {
		return err
	}
	datas := make([]*webrtc.DataChannel, nCh)
	for i := range datas {
		if datas[i], err = pc.CreateDataChannel(fmt.Sprintf("data-%d", i), nil); err != nil {
			return err
		}
	}

	var ackOnce sync.Once
	ackCh := make(chan struct{})
	ctrl.OnMessage(func(m webrtc.DataChannelMessage) {
		var msg ctrlMsg
		if json.Unmarshal(m.Data, &msg) == nil && msg.Type == "ack" {
			ackOnce.Do(func() { close(ackCh) })
		}
	})

	openChs := make([]chan struct{}, 0, nCh+1)
	for _, dc := range append(datas, ctrl) {
		c := make(chan struct{})
		openChs = append(openChs, c)
		dc.OnOpen(func() { close(c) })
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	var cands []iceCand
	gatherDone := gatherCandidates(pc, &cands)
	if err = pc.SetLocalDescription(offer); err != nil {
		return err
	}
	<-gatherDone

	code := encodeCode(encodePayload(sess.Seed, cands))

	fmt.Printf("session %s  (%s)\n", sess.Name, root)
	fmt.Print("offer code:\n")
	if err = publishCode(code, codeFile, "offer"); err != nil {
		return err
	}

	answerCode, err := readCode(codeFile, "answer", "paste answer code: ")
	if err != nil {
		return err
	}
	_, ansCands, err := decodeCode(answerCode)
	if err != nil {
		return err
	}
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  buildSDP(sess.Ufrag, sess.Pwd, sess.Fp, "active", candStrings(ansCands)),
	}
	if err = pc.SetRemoteDescription(answer); err != nil {
		return err
	}

	for _, c := range openChs {
		<-c
	}

	start := time.Now()
	paths := make(chan string, 64)
	walkErr := make(chan error, 1)
	go func() {
		defer close(paths)
		err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type().IsRegular() {
				paths <- p
			}
			return nil
		})
		if err != nil {
			walkErr <- err
		}
	}()

	var wg sync.WaitGroup
	var nextID atomic.Uint32
	var sentFiles atomic.Uint64
	var sentBytes atomic.Int64
	for _, dc := range datas {
		wg.Add(1)
		go func(dc *webrtc.DataChannel) {
			defer wg.Done()
			for p := range paths {
				if err := sendFile(dc, abs, top, p, &nextID, &sentFiles, &sentBytes); err != nil {
					fail(err)
					return
				}
			}
		}(dc)
	}

	stopTicker := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				fmt.Printf("\rsent %d files, %s (%s/s)",
					sentFiles.Load(), humanBytes(sentBytes.Load()),
					humanBytes(int64(float64(sentBytes.Load())/time.Since(start).Seconds())))
			case <-stopTicker:
				return
			}
		}
	}()

	streamDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(streamDone)
	}()
	select {
	case <-streamDone:
	case err = <-walkErr:
		close(stopTicker)
		return err
	case err = <-errCh:
		close(stopTicker)
		return err
	}

	if err = ctrl.SendText(`{"type":"end"}`); err != nil {
		return err
	}
	select {
	case <-ackCh:
	case err = <-errCh:
		close(stopTicker)
		return err
	}
	close(stopTicker)

	elapsed := time.Since(start).Round(time.Millisecond)
	fmt.Printf("\rdone: %d files, %s in %s (%s/s)          \n",
		sentFiles.Load(), humanBytes(sentBytes.Load()), elapsed,
		humanBytes(int64(float64(sentBytes.Load())/elapsed.Seconds())))
	return nil
}

func sendFile(dc *webrtc.DataChannel, root, top, path string, nextID *atomic.Uint32, sentFiles *atomic.Uint64, sentBytes *atomic.Int64) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	name := filepath.ToSlash(filepath.Join(top, rel))
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	id := nextID.Add(1) - 1
	if err = sendThrottled(dc, announceFrame(id, name, fi.Size(), uint32(fi.Mode().Perm()))); err != nil {
		return err
	}
	if fi.Size() == 0 {
		sentFiles.Add(1)
		return nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	off := int64(0)
	for off < fi.Size() {
		n := min(int64(chunkSize), fi.Size()-off)
		buf := make([]byte, n)
		if _, err = io.ReadFull(fh, buf); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err = sendThrottled(dc, chunkFrame(uint32(id), off, buf)); err != nil {
			return err
		}
		off += n
		sentBytes.Add(n)
	}
	sentFiles.Add(1)
	return nil
}

func sendThrottled(dc *webrtc.DataChannel, frame []byte) error {
	for dc.BufferedAmount() > bufThreshold {
		time.Sleep(2 * time.Millisecond)
	}
	return dc.Send(frame)
}
