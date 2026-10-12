package main

import (
	"fmt"
	"strings"

	"github.com/pion/webrtc/v4"
)

const (
	codeVersion = "tp3"
	stunURL     = "stun:stun.l.google.com:19302"
)

func buildSDP(ufrag, pwd, fp, setup string, cands []string) string {
	var b strings.Builder
	b.WriteString("v=0\r\n")
	b.WriteString("o=- 4611731400430106496 2 IN IP4 127.0.0.1\r\n")
	b.WriteString("s=-\r\n")
	b.WriteString("t=0 0\r\n")
	b.WriteString("a=group:BUNDLE 0\r\n")
	b.WriteString("m=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n")
	b.WriteString("c=IN IP4 0.0.0.0\r\n")
	fmt.Fprintf(&b, "a=ice-ufrag:%s\r\n", ufrag)
	fmt.Fprintf(&b, "a=ice-pwd:%s\r\n", pwd)
	b.WriteString("a=ice-options:trickle\r\n")
	fmt.Fprintf(&b, "a=fingerprint:sha-256 %s\r\n", fp)
	fmt.Fprintf(&b, "a=setup:%s\r\n", setup)
	b.WriteString("a=mid:0\r\n")
	b.WriteString("a=sctp-port:5000\r\n")
	b.WriteString("a=max-message-size:262144\r\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "a=%s\r\n", c)
	}
	return b.String()
}

// gatherCandidates registers the callback and returns once ICE gathering is done.
func gatherCandidates(pc *webrtc.PeerConnection, out *[]iceCand) <-chan struct{} {
	done := make(chan struct{})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			close(done)
			return
		}
		if ic, ok := parseCand(c.ToJSON().Candidate); ok {
			*out = append(*out, ic)
		}
	})
	return done
}
