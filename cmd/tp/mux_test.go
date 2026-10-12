package main

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestLaneHandshake(t *testing.T) {
	sessA, err := newSession()
	if err != nil {
		t.Fatal(err)
	}
	sessB, err := resumeSession(sessA.Seed)
	if err != nil {
		t.Fatal(err)
	}

	pcA, err := sessA.newPeerConnection(0, "")
	if err != nil {
		t.Fatal(err)
	}
	pcB, err := sessB.newPeerConnection(0, "")
	if err != nil {
		t.Fatal(err)
	}

	var candsA, candsB []iceCand
	ga := gatherCandidates(pcA, &candsA)
	gb := gatherCandidates(pcB, &candsB)

	dc, err := pcA.CreateDataChannel("data-0", nil)
	if err != nil {
		t.Fatal(err)
	}
	dcOpened := make(chan struct{})
	dc.OnOpen(func() { close(dcOpened) })

	offer, err := pcA.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = pcA.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-ga
	t.Logf("A cands: %v", candStrings(candsA))

	ufrag, pwd := sessA.creds(0)
	offerSDP := buildSDP(ufrag, pwd, sessA.Fp, "actpass", candStrings(candsA))
	if err = pcB.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer, SDP: offerSDP,
	}); err != nil {
		t.Fatal(err)
	}
	answer, err := pcB.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = pcB.SetLocalDescription(answer); err != nil {
		t.Fatal(err)
	}
	<-gb
	t.Logf("B cands: %v", candStrings(candsB))

	answerSDP := buildSDP(ufrag, pwd, sessA.Fp, "active", candStrings(candsB))
	if err = pcA.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer, SDP: answerSDP,
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pcA.ICEConnectionState() == webrtc.ICEConnectionStateConnected {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pcA.ICEConnectionState() != webrtc.ICEConnectionStateConnected {
		t.Fatalf("never connected: %s", pcA.ICEConnectionState())
	}
	select {
	case <-dcOpened:
	case <-time.After(5 * time.Second):
		t.Fatal("data channel never opened")
	}
}
