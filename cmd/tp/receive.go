package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
)

type recvFile struct {
	f    *os.File
	size int64
	got  int64
}

type receiver struct {
	mu        sync.Mutex
	dir       string
	files     map[uint32]*recvFile
	filesSeen int
	filesDone int
	skipped   int
	got       int64
	ended     bool
	start     time.Time
	ctrl      *webrtc.DataChannel
	done      chan struct{}
	doneOnce  sync.Once
	errCh     chan error
}

func (r *receiver) complete() {
	r.doneOnce.Do(func() { close(r.done) })
}

func (r *receiver) fail(err error) {
	select {
	case r.errCh <- err:
	default:
	}
}

func runReceive(outDir, codeFile, public string, upnp bool) error {
	offerCode, err := readCode(codeFile, "offer", "paste offer code: ")
	if err != nil {
		return err
	}
	seed, offerPcs, err := decodeCode(offerCode)
	if err != nil {
		return err
	}
	nLanes := len(offerPcs)
	if public != "" {
		nLanes = 1 // one manually forwarded port can carry one lane
	}

	pins, stop := resolveEndpoints(public, upnp, nLanes)
	if stop != nil {
		defer stop()
	}
	if len(pins) > 0 {
		for _, pin := range pins {
			fmt.Fprintln(os.Stderr, "tp: reachable at", pin)
		}
	} else {
		go fmt.Fprintln(os.Stderr, "tp: local nat:", probeNAT())
	}

	sess, err := resumeSession(seed)
	if err != nil {
		return err
	}

	absDir, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	recv := &receiver{
		dir:   absDir,
		files: make(map[uint32]*recvFile),
		done:  make(chan struct{}),
		errCh: make(chan error, 1),
		start: time.Now(),
	}
	ice := &iceState{}
	var open int32
	var pcs []*webrtc.PeerConnection
	var allCands [][]iceCand

	addLane := func(i int) error {
		pin := ""
		if len(pins) > 0 {
			pin = pins[i]
		}
		pc, err := sess.newPeerConnection(i, pin)
		if err != nil {
			return err
		}
		pcs = append(pcs, pc)
		pc.OnICEConnectionStateChange(ice.set)
		pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
			if s == webrtc.PeerConnectionStateFailed && atomic.LoadInt32(&open) == 0 {
				reportConnFailure(ice.get(), flatten(allCands), flatten(offerPcs))
				recv.fail(errors.New("connection failed"))
			}
		})
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			switch {
			case dc.Label() == "ctrl":
				recv.mu.Lock()
				recv.ctrl = dc
				recv.mu.Unlock()
				dc.OnMessage(func(m webrtc.DataChannelMessage) { recv.handleCtrl(m.Data) })
			case strings.HasPrefix(dc.Label(), "data-"):
				atomic.AddInt32(&open, 1)
				dc.OnMessage(func(m webrtc.DataChannelMessage) { recv.handleData(dc, m.Data) })
			}
		})
		ufrag, pwd := sess.creds(i)
		offer := webrtc.SessionDescription{
			Type: webrtc.SDPTypeOffer,
			SDP:  buildSDP(ufrag, pwd, sess.Fp, "actpass", candStrings(offerPcs[i])),
		}
		if err = pc.SetRemoteDescription(offer); err != nil {
			return err
		}
		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			return err
		}
		var laneCands []iceCand
		gatherDone := gatherCandidates(pc, &laneCands)
		if err = pc.SetLocalDescription(answer); err != nil {
			return err
		}
		<-gatherDone
		allCands = append(allCands, laneCands)
		return nil
	}

	for i := 0; i < nLanes; i++ {
		if err = addLane(i); err != nil {
			return err
		}
	}

	fmt.Printf("session %s\n", sess.Name)
	fmt.Print("answer code:\n")
	if err = publishCode(encodeCode(encodePayload(nil, allCands)), codeFile, "answer"); err != nil {
		return err
	}

	stopTicker := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				recv.mu.Lock()
				fmt.Printf("\r%d files, %s (%s/s)", recv.filesDone,
					humanBytes(recv.got),
					humanBytes(int64(float64(recv.got)/time.Since(recv.start).Seconds())))
				recv.mu.Unlock()
			case <-stopTicker:
				return
			}
		}
	}()

	select {
	case <-recv.done:
	case err = <-recv.errCh:
		close(stopTicker)
		return err
	}
	close(stopTicker)

	recv.mu.Lock()
	ctrl := recv.ctrl
	recv.mu.Unlock()
	if ctrl != nil {
		ctrl.SendText(`{"type":"ack"}`)
		time.Sleep(200 * time.Millisecond)
	}

	for _, pc := range pcs {
		pc.Close()
	}

	fmt.Println()
	recv.mu.Lock()
	defer recv.mu.Unlock()
	elapsed := time.Since(recv.start).Round(time.Millisecond)
	fmt.Printf("done: %d files (%d skipped), %s in %s (%s/s)\n",
		recv.filesDone, recv.skipped, humanBytes(recv.got), elapsed,
		humanBytes(int64(float64(recv.got)/elapsed.Seconds())))
	return nil
}

func (r *receiver) handleCtrl(data []byte) {
	var msg ctrlMsg
	if err := json.Unmarshal(data, &msg); err != nil || msg.Type != "end" {
		return
	}
	r.mu.Lock()
	r.ended = true
	ok := r.filesDone == r.filesSeen
	r.mu.Unlock()
	if ok {
		r.complete()
	}
}

func (r *receiver) handleData(dc *webrtc.DataChannel, data []byte) {
	switch {
	case len(data) >= 35 && data[0] == frameAnnounce:
		r.handleAnnounce(dc, data)
	case len(data) >= 13 && data[0] == frameChunk:
		r.handleChunk(data)
	}
}

func (r *receiver) handleAnnounce(dc *webrtc.DataChannel, data []byte) {
	id := binary.BigEndian.Uint32(data[1:5])
	size := int64(binary.BigEndian.Uint64(data[5:13]))
	mode := binary.BigEndian.Uint32(data[13:17])
	var digest [hashLen]byte
	copy(digest[:], data[17:33])
	nameLen := int(binary.BigEndian.Uint16(data[33:35]))
	if len(data) < 35+nameLen {
		r.fail(errors.New("truncated announce"))
		return
	}
	name := string(data[35 : 35+nameLen])
	full := filepath.Join(r.dir, filepath.FromSlash(name))
	if full != filepath.Clean(r.dir) && !strings.HasPrefix(full, filepath.Clean(r.dir)+string(os.PathSeparator)) {
		r.fail(fmt.Errorf("unsafe path %q", name))
		return
	}

	r.mu.Lock()
	if r.files[id] != nil {
		r.mu.Unlock()
		r.fail(fmt.Errorf("duplicate file id %d", id))
		return
	}
	r.filesSeen++
	if r.start.IsZero() {
		r.start = time.Now()
	}
	r.mu.Unlock()

	if fi, err := os.Stat(full); err == nil && fi.Mode().IsRegular() && fi.Size() == size {
		if ed, err := fileDigest(full); err == nil && ed == digest {
			os.Chmod(full, os.FileMode(mode))
			r.mu.Lock()
			r.filesDone++
			r.skipped++
			r.checkComplete()
			r.mu.Unlock()
			if err = dc.Send(verdictFrame(id, true)); err != nil {
				r.fail(err)
			}
			return
		}
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.fail(err)
		return
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		r.fail(err)
		return
	}
	if err = f.Chmod(os.FileMode(mode)); err != nil {
		f.Close()
		r.fail(err)
		return
	}
	r.mu.Lock()
	r.files[id] = &recvFile{f: f, size: size}
	if size == 0 {
		f.Close()
		delete(r.files, id)
		r.filesDone++
		r.checkComplete()
	}
	r.mu.Unlock()
	if err = dc.Send(verdictFrame(id, false)); err != nil {
		r.fail(err)
	}
}

func (r *receiver) handleChunk(data []byte) {
	id := binary.BigEndian.Uint32(data[1:5])
	off := int64(binary.BigEndian.Uint64(data[5:13]))
	r.mu.Lock()
	rf := r.files[id]
	if rf == nil {
		r.mu.Unlock()
		return
	}
	n, err := rf.f.WriteAt(data[13:], off)
	if err != nil {
		r.mu.Unlock()
		r.fail(err)
		return
	}
	rf.got += int64(n)
	r.got += int64(n)
	if rf.got == rf.size {
		rf.f.Close()
		delete(r.files, id)
		r.filesDone++
		r.checkComplete()
	}
	r.mu.Unlock()
}

func (r *receiver) checkComplete() {
	if r.ended && r.filesDone == r.filesSeen {
		r.complete()
	}
}
