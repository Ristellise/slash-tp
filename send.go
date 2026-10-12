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

	files, err := walkFiles(abs)
	if err != nil {
		return err
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}

	pc, err := newPeerConnection()
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

	var readyOnce, ackOnce sync.Once
	readyCh, ackCh := make(chan struct{}), make(chan struct{})
	ctrl.OnMessage(func(m webrtc.DataChannelMessage) {
		var msg ctrlMsg
		if json.Unmarshal(m.Data, &msg) == nil {
			switch msg.Type {
			case "ready":
				readyOnce.Do(func() { close(readyCh) })
			case "ack":
				ackOnce.Do(func() { close(ackCh) })
			}
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
	var cands []string
	gatherDone := gatherCandidates(pc, &cands)
	if err = pc.SetLocalDescription(offer); err != nil {
		return err
	}
	<-gatherDone

	ufrag, pwd, fp, setup, err := extractSDP(offer.SDP)
	if err != nil {
		return err
	}
	name := generateSessionName()
	code, err := encodeCode(name, sdpPayload{U: ufrag, P: pwd, F: fp, S: setup, C: cands})
	if err != nil {
		return err
	}

	fmt.Printf("session %s  (%d files, %s)\n", name, len(files), humanBytes(total))
	fmt.Print("offer code:\n")
	if err = publishCode(code, codeFile, "offer"); err != nil {
		return err
	}

	answerCode, err := readCode(codeFile, "answer", "paste answer code: ")
	if err != nil {
		return err
	}
	ansName, ansPayload, err := decodeCode(answerCode)
	if err != nil {
		return err
	}
	if ansName != name {
		return fmt.Errorf("session mismatch: expected %s, got %s", name, ansName)
	}
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: buildSDP(ansPayload)}); err != nil {
		return err
	}

	for _, c := range openChs {
		<-c
	}

	manifest := manifestMsg{Type: "manifest", Top: top, Chunk: chunkSize, Files: files}
	if raw, err := json.Marshal(manifest); err == nil {
		if err = ctrl.Send(raw); err != nil {
			return err
		}
	}
	select {
	case <-readyCh:
	case err = <-errCh:
		return err
	}

	start := time.Now()
	var queued atomic.Int64
	tasks := make(chan []byte, nCh*4)
	prodErr := make(chan error, 1)
	go func() {
		defer close(tasks)
		for _, fe := range files {
			if fe.Size == 0 {
				continue
			}
			fh, err := os.Open(filepath.Join(abs, filepath.FromSlash(fe.Name)))
			if err != nil {
				prodErr <- err
				return
			}
			off := int64(0)
			for off < fe.Size {
				n := min(int64(chunkSize), fe.Size-off)
				frame := make([]byte, frameHeader+n)
				framePut(frame, fe.ID, off)
				if _, err = io.ReadFull(fh, frame[frameHeader:]); err != nil {
					fh.Close()
					prodErr <- fmt.Errorf("%s: %w", fe.Name, err)
					return
				}
				tasks <- frame
				queued.Add(n)
				off += n
			}
			fh.Close()
		}
	}()

	var wg sync.WaitGroup
	for _, dc := range datas {
		wg.Add(1)
		go func(dc *webrtc.DataChannel) {
			defer wg.Done()
			for frame := range tasks {
				for dc.BufferedAmount() > bufThreshold {
					time.Sleep(2 * time.Millisecond)
				}
				if err := dc.Send(frame); err != nil {
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
				q := queued.Load()
				line := fmt.Sprintf("\rsent %s / %s", humanBytes(q), humanBytes(total))
				if total > 0 {
					line += fmt.Sprintf(" (%.0f%%)", 100*float64(q)/float64(total))
				}
				fmt.Print(line)
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
	case err = <-prodErr:
		close(stopTicker)
		return err
	case err = <-errCh:
		close(stopTicker)
		return err
	}

	select {
	case <-ackCh:
	case err = <-errCh:
		close(stopTicker)
		return err
	}
	close(stopTicker)

	elapsed := time.Since(start)
	fmt.Printf("\rdone: %d files, %s in %s (%s/s)          \n",
		len(files), humanBytes(total), elapsed.Round(time.Millisecond), humanBytes(int64(float64(total)/elapsed.Seconds())))
	return nil
}

func walkFiles(root string) ([]fileEntry, error) {
	var out []fileEntry
	id := uint32(0)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, fileEntry{
			ID:   id,
			Name: filepath.ToSlash(rel),
			Size: fi.Size(),
			Mode: uint32(fi.Mode().Perm()),
		})
		id++
		return nil
	})
	return out, err
}
