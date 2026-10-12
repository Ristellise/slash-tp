package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

type recvFile struct {
	f    *os.File
	size int64
	got  int64
}

type receiver struct {
	mu         sync.Mutex
	dir        string
	files      map[uint32]*recvFile
	total      int64
	remaining  int64
	filesDone  int
	filesTotal int
	ctrl       *webrtc.DataChannel
	start      time.Time
	done       chan struct{}
	doneOnce   sync.Once
	errCh      chan error
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

func runReceive(outDir, codeFile string) error {
	offerCode, err := readCode(codeFile, "offer", "paste offer code: ")
	if err != nil {
		return err
	}
	seed, offerCands, err := decodeCode(offerCode)
	if err != nil {
		return err
	}
	sess, err := resumeSession(seed)
	if err != nil {
		return err
	}

	pc, err := sess.newPeerConnection()
	if err != nil {
		return err
	}
	defer pc.Close()

	errCh := make(chan error, 1)
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed {
			select {
			case errCh <- errors.New("connection failed"):
			default:
			}
		}
	})

	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  buildSDP(sess.Ufrag, sess.Pwd, sess.Fp, "actpass", candStrings(offerCands)),
	}
	if err = pc.SetRemoteDescription(offer); err != nil {
		return err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	var cands []iceCand
	gatherDone := gatherCandidates(pc, &cands)
	if err = pc.SetLocalDescription(answer); err != nil {
		return err
	}
	<-gatherDone

	fmt.Printf("session %s\n", sess.Name)
	fmt.Print("answer code:\n")
	if err = publishCode(encodeCode(encodePayload(nil, cands)), codeFile, "answer"); err != nil {
		return err
	}

	recv := &receiver{
		files: make(map[uint32]*recvFile),
		dir:   outDir,
		done:  make(chan struct{}),
		errCh: make(chan error, 1),
	}
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch {
		case dc.Label() == "ctrl":
			recv.mu.Lock()
			recv.ctrl = dc
			recv.mu.Unlock()
			dc.OnMessage(func(m webrtc.DataChannelMessage) { recv.handleCtrl(m.Data) })
		case strings.HasPrefix(dc.Label(), "data-"):
			dc.OnMessage(func(m webrtc.DataChannelMessage) { recv.handleFrame(m.Data) })
		}
	})

	stopTicker := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				recv.mu.Lock()
				line := fmt.Sprintf("\r%d/%d files  %s / %s", recv.filesDone, recv.filesTotal,
					humanBytes(recv.total-recv.remaining), humanBytes(recv.total))
				if !recv.start.IsZero() && recv.total > 0 {
					line += fmt.Sprintf("  (%s/s)", humanBytes(int64(float64(recv.total-recv.remaining)/time.Since(recv.start).Seconds())))
				}
				recv.mu.Unlock()
				fmt.Print(line)
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
	case err = <-errCh:
		close(stopTicker)
		return err
	}
	close(stopTicker)

	recv.mu.Lock()
	ctrl := recv.ctrl
	recv.mu.Unlock()
	if ctrl != nil {
		ctrl.SendText(`{"type":"ack"}`)
	}

	fmt.Println()
	recv.summary()
	time.Sleep(200 * time.Millisecond)
	return nil
}

func (r *receiver) handleCtrl(data []byte) {
	var m manifestMsg
	if err := json.Unmarshal(data, &m); err != nil || m.Type != "manifest" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	root := filepath.Join(r.dir, filepath.FromSlash(m.Top))
	base := filepath.Clean(r.dir) + string(os.PathSeparator)
	for _, fe := range m.Files {
		full := filepath.Join(root, filepath.FromSlash(fe.Name))
		if full != filepath.Clean(r.dir) && !strings.HasPrefix(full, base) {
			r.fail(fmt.Errorf("skipping unsafe path %q", fe.Name))
			continue
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
		if err = f.Chmod(fs.FileMode(fe.Mode)); err != nil {
			f.Close()
			r.fail(err)
			return
		}
		r.files[fe.ID] = &recvFile{f: f, size: fe.Size}
		r.total += fe.Size
		r.remaining += fe.Size
		r.filesTotal++
	}
	r.start = time.Now()
	if r.remaining == 0 {
		r.complete()
	}
	if r.ctrl != nil {
		r.ctrl.SendText(`{"type":"ready"}`)
	}
}

func (r *receiver) handleFrame(data []byte) {
	if len(data) <= frameHeader {
		return
	}
	id, off := frameGet(data)
	r.mu.Lock()
	rf, ok := r.files[id]
	if ok {
		n, err := rf.f.WriteAt(data[frameHeader:], off)
		if err != nil {
			r.fail(err)
			r.mu.Unlock()
			return
		}
		r.remaining -= int64(n)
		rf.got += int64(n)
		if rf.got == rf.size {
			r.filesDone++
		}
		if r.remaining <= 0 {
			r.complete()
		}
	}
	r.mu.Unlock()
}

func (r *receiver) summary() {
	r.mu.Lock()
	defer r.mu.Unlock()
	elapsed := time.Since(r.start).Round(time.Millisecond)
	speed := humanBytes(0)
	if elapsed > 0 && r.total > 0 {
		speed = humanBytes(int64(float64(r.total) / elapsed.Seconds()))
	}
	fmt.Printf("done: %d files, %s in %s (%s/s)\n", r.filesTotal, humanBytes(r.total), elapsed, speed)
}
