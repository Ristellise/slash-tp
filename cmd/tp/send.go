package main

import (
	"encoding/binary"
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

type pendingFile struct {
	id   uint32
	path string
	name string
	size int64
}

type verdict struct {
	id   uint32
	have bool
}

// lanes tracks open data channels so file chunks can stripe across them.
type lanes struct {
	mu    sync.RWMutex
	dcs   []*webrtc.DataChannel
	width int32
}

func (l *lanes) add(dc *webrtc.DataChannel) {
	l.mu.Lock()
	l.dcs = append(l.dcs, dc)
	l.mu.Unlock()
}

// pick returns the least-backlogged lane among the first width lanes.
func (l *lanes) pick() *webrtc.DataChannel {
	w := int(atomic.LoadInt32(&l.width))
	l.mu.RLock()
	defer l.mu.RUnlock()
	best := l.dcs[0]
	for _, dc := range l.dcs[1:min(w, len(l.dcs))] {
		if dc.BufferedAmount() < best.BufferedAmount() {
			best = dc
		}
	}
	return best
}

func (l *lanes) count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.dcs)
}

func runSend(root string, nMax int, codeFile, public string, upnp bool) error {
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

	// multi-lane only applies to automatic upnp forwarding; a manual --public
	// port or plain STUN mode runs a single lane
	nLanes := 1
	if public == "" && upnp {
		nLanes = nMax
	}
	pins, stop := resolveEndpoints(public, upnp, nLanes)
	if stop != nil {
		defer stop()
	}
	if len(pins) == 1 && pins[0] != public {
		nLanes = 1 // upnp failed, single-lane fallback
	}
	if len(pins) > 0 {
		for _, pin := range pins {
			fmt.Fprintln(os.Stderr, "tp: reachable at", pin)
		}
	} else {
		go fmt.Fprintln(os.Stderr, "tp: local nat:", probeNAT())
	}

	sess, err := newSession()
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	fail := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}

	var remotePcs [][]iceCand
	ice := &iceState{}
	var open int32

	openLanes := &lanes{width: 1 << 30} // all lanes until autoscale decides
	opens := make(chan struct{}, 64)
	paths := make(chan string, 64)
	walkErr := make(chan error, 1)
	var wg sync.WaitGroup
	var nextID atomic.Uint32
	var sentFiles, skippedFiles atomic.Uint64
	var sentBytes atomic.Int64
	startProd := func(dc *webrtc.DataChannel) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := produce(dc, abs, top, paths, openLanes, &nextID, &sentFiles, &skippedFiles, &sentBytes); err != nil {
				fail(err)
			}
		}()
	}

	ctrlCh := make(chan *webrtc.DataChannel, 1)
	var pcs []*webrtc.PeerConnection
	var allCands [][]iceCand
	addLane := func(i int, nCh int) error {
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
				reportConnFailure(ice.get(), flatten(allCands), flatten(remotePcs))
				fail(errors.New("connection failed"))
			}
		})
		if i == 0 {
			ctrl, err := pc.CreateDataChannel("ctrl", nil)
			if err != nil {
				return err
			}
			ctrlCh <- ctrl
		}
		for j := 0; j < nCh; j++ {
			label := fmt.Sprintf("data-%d", j)
			if pin != "" {
				label = fmt.Sprintf("data-%d", i)
			}
			dc, err := pc.CreateDataChannel(label, nil)
			if err != nil {
				return err
			}
			dc.OnOpen(func() {
				atomic.AddInt32(&open, 1)
				openLanes.add(dc)
				select {
				case opens <- struct{}{}:
				default:
				}
				startProd(dc)
			})
		}
		offer, err := pc.CreateOffer(nil)
		if err != nil {
			return err
		}
		var laneCands []iceCand
		gatherDone := gatherCandidates(pc, &laneCands)
		if err = pc.SetLocalDescription(offer); err != nil {
			return err
		}
		<-gatherDone
		allCands = append(allCands, laneCands)
		return nil
	}

	chPerLane := nMax
	if len(pins) > 0 {
		chPerLane = 1
	}
	for i := 0; i < nLanes; i++ {
		if err = addLane(i, chPerLane); err != nil {
			return err
		}
	}

	code := encodeCode(encodePayload(sess.Seed, allCands))
	fmt.Printf("session %s  (%s)\n", sess.Name, root)
	fmt.Print("offer code:\n")
	if err = publishCode(code, codeFile, "offer"); err != nil {
		return err
	}

	answerCode, err := readCode(codeFile, "answer", "paste answer code: ")
	if err != nil {
		return err
	}
	_, ansPcs, err := decodeCode(answerCode)
	if err != nil {
		return err
	}
	remotePcs = ansPcs
	if len(ansPcs) != len(pcs) {
		return fmt.Errorf("peer reported %d lanes, expected %d", len(ansPcs), len(pcs))
	}
	for i, pc := range pcs {
		ufrag, pwd := sess.creds(i)
		answer := webrtc.SessionDescription{
			Type: webrtc.SDPTypeAnswer,
			SDP:  buildSDP(ufrag, pwd, sess.Fp, "active", candStrings(ansPcs[i])),
		}
		if err = pc.SetRemoteDescription(answer); err != nil {
			return fmt.Errorf("setremote %d: %w", i, err)
		}
	}

	ctrl := <-ctrlCh
	var ackOnce sync.Once
	ackCh := make(chan struct{})
	ctrl.OnMessage(func(m webrtc.DataChannelMessage) {
		var msg ctrlMsg
		if json.Unmarshal(m.Data, &msg) == nil && msg.Type == "ack" {
			ackOnce.Do(func() { close(ackCh) })
		}
	})

	// wait for lanes to connect: first one is required, the rest get 2s grace
	<-opens
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && openLanes.count() < chPerLane*nLanes {
		select {
		case <-opens:
		case <-time.After(time.Until(deadline)):
		}
	}
	if openLanes.count() == 0 {
		return errors.New("no lanes connected")
	}

	start := time.Now()
	go func() {
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
		close(paths)
	}()

	streamDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(streamDone)
	}()

	// autoscale: widen the striping set while throughput keeps improving
	if nLanes > 1 {
		atomic.StoreInt32(&openLanes.width, 2)
		go func() {
			last := float64(0)
			for {
				b := sentBytes.Load()
				select {
				case <-streamDone:
					return
				case <-time.After(4 * time.Second):
				}
				rate := float64(sentBytes.Load()-b) / 4
				w := int(atomic.LoadInt32(&openLanes.width))
				n := openLanes.count()
				if n <= w || n >= nLanes {
					continue
				}
				if rate == 0 || (last > 0 && rate < last*1.15) {
					continue
				}
				last = max(last, rate)
				atomic.StoreInt32(&openLanes.width, int32(min(n, w*2)))
			}
		}()
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

	for _, pc := range pcs {
		pc.Close()
	}
	elapsed := time.Since(start).Round(time.Millisecond)
	fmt.Printf("\rdone: %d files (%d skipped), %s in %s (%s/s)          \n",
		sentFiles.Load(), skippedFiles.Load(), humanBytes(sentBytes.Load()), elapsed,
		humanBytes(int64(float64(sentBytes.Load())/elapsed.Seconds())))
	return nil
}

func flatten(pcs [][]iceCand) []iceCand {
	var out []iceCand
	for _, c := range pcs {
		out = append(out, c...)
	}
	return out
}

func produce(dc *webrtc.DataChannel, root, top string, paths <-chan string, openLanes *lanes,
	nextID *atomic.Uint32, sentFiles, skippedFiles *atomic.Uint64, sentBytes *atomic.Int64) error {
	inbox := make(chan verdict, sendWindow*2)
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		d := m.Data
		if len(d) == 6 && d[0] == frameVerdict {
			inbox <- verdict{id: binary.BigEndian.Uint32(d[1:5]), have: d[5] == 1}
		}
	})

	var pending []pendingFile
	pathsOpen := true
	for pathsOpen || len(pending) > 0 {
		if pathsOpen && len(pending) < sendWindow {
			select {
			case p, ok := <-paths:
				if !ok {
					pathsOpen = false
				} else if err := announce(dc, root, top, p, nextID, &pending); err != nil {
					return err
				}
			case v := <-inbox:
				if err := consume(openLanes, &pending, v, sentFiles, skippedFiles, sentBytes); err != nil {
					return err
				}
			}
		} else {
			v := <-inbox
			if err := consume(openLanes, &pending, v, sentFiles, skippedFiles, sentBytes); err != nil {
				return err
			}
		}
	}
	return nil
}

func announce(dc *webrtc.DataChannel, root, top, path string, nextID *atomic.Uint32, pending *[]pendingFile) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	name := filepath.ToSlash(filepath.Join(top, rel))
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	first, err := fileDigest(path)
	if err != nil {
		return err
	}
	id := nextID.Add(1) - 1
	if err = sendThrottled(dc, announceFrame(uint32(id), name, fi.Size(), uint32(fi.Mode().Perm()), first)); err != nil {
		return err
	}
	*pending = append(*pending, pendingFile{id: uint32(id), path: path, name: name, size: fi.Size()})
	return nil
}

func consume(openLanes *lanes, pending *[]pendingFile, v verdict,
	sentFiles, skippedFiles *atomic.Uint64, sentBytes *atomic.Int64) error {
	p := (*pending)[0]
	if v.id != p.id {
		return fmt.Errorf("verdict out of order: got %d, want %d", v.id, p.id)
	}
	*pending = (*pending)[1:]
	if !v.have {
		if err := streamFile(openLanes, p, sentBytes); err != nil {
			return err
		}
	} else {
		skippedFiles.Add(1)
	}
	sentFiles.Add(1)
	return nil
}

func streamFile(openLanes *lanes, p pendingFile, sentBytes *atomic.Int64) error {
	if p.size == 0 {
		return nil
	}
	fh, err := os.Open(p.path)
	if err != nil {
		return err
	}
	defer fh.Close()
	off := int64(0)
	for off < p.size {
		n := min(int64(chunkSize), p.size-off)
		buf := make([]byte, n)
		if _, err = io.ReadFull(fh, buf); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		dc := openLanes.pick()
		if err = sendThrottled(dc, chunkFrame(p.id, off, buf)); err != nil {
			return err
		}
		off += n
		sentBytes.Add(n)
	}
	return nil
}

func sendThrottled(dc *webrtc.DataChannel, frame []byte) error {
	for dc.BufferedAmount() > bufThreshold {
		time.Sleep(2 * time.Millisecond)
	}
	return dc.Send(frame)
}
