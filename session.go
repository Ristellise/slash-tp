package main

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/pion/webrtc/v4"
)

const (
	codeVersion = "tp1"
	stunURL     = "stun:stun.l.google.com:19302"
)

// sdpPayload carries everything a data-channel-only WebRTC session needs.
type sdpPayload struct {
	U string   `json:"u"` // ice-ufrag
	P string   `json:"p"` // ice-pwd
	F string   `json:"f"` // dtls fingerprint, hex without colons
	S string   `json:"s"` // setup role
	C []string `json:"c"` // ICE candidates, "candidate:..." lines
}

func encodeCode(name string, p sdpPayload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(raw); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return codeVersion + "#" + name + "#" + base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

var codeRe = regexp.MustCompile(codeVersion + `#[^#\s]+#[A-Za-z0-9_-]+`)

func decodeCode(code string) (string, sdpPayload, error) {
	var name string
	var p sdpPayload
	m := codeRe.FindString(strings.TrimSpace(code))
	if m == "" {
		return name, p, errors.New("no session code found (expected tp1#name#data)")
	}
	parts := strings.Split(m, "#")
	name = parts[1]
	data, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return name, p, err
	}
	r := flate.NewReader(bytes.NewReader(data))
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		return name, p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return name, p, err
	}
	if p.U == "" || p.P == "" || p.F == "" || p.S == "" {
		return name, p, errors.New("session code is missing SDP fields")
	}
	return name, p, nil
}

func extractSDP(sdp string) (ufrag, pwd, fp, setup string, err error) {
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "a=ice-ufrag:"):
			ufrag = strings.TrimPrefix(line, "a=ice-ufrag:")
		case strings.HasPrefix(line, "a=ice-pwd:"):
			pwd = strings.TrimPrefix(line, "a=ice-pwd:")
		case strings.HasPrefix(line, "a=fingerprint:sha-256 "):
			fp = strings.ReplaceAll(strings.TrimPrefix(line, "a=fingerprint:sha-256 "), ":", "")
		case strings.HasPrefix(line, "a=setup:"):
			setup = strings.TrimPrefix(line, "a=setup:")
		}
	}
	if ufrag == "" || pwd == "" || fp == "" || setup == "" {
		err = errors.New("could not extract ICE credentials from SDP")
	}
	return
}

func colonFP(hex string) string {
	var b strings.Builder
	for i := 0; i+1 < len(hex); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hex[i : i+2])
	}
	return b.String()
}

func buildSDP(p sdpPayload) string {
	var b strings.Builder
	b.WriteString("v=0\r\n")
	b.WriteString("o=- 4611731400430106496 2 IN IP4 127.0.0.1\r\n")
	b.WriteString("s=-\r\n")
	b.WriteString("t=0 0\r\n")
	b.WriteString("a=group:BUNDLE 0\r\n")
	b.WriteString("m=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n")
	b.WriteString("c=IN IP4 0.0.0.0\r\n")
	fmt.Fprintf(&b, "a=ice-ufrag:%s\r\n", p.U)
	fmt.Fprintf(&b, "a=ice-pwd:%s\r\n", p.P)
	b.WriteString("a=ice-options:trickle\r\n")
	fmt.Fprintf(&b, "a=fingerprint:sha-256 %s\r\n", colonFP(p.F))
	fmt.Fprintf(&b, "a=setup:%s\r\n", p.S)
	b.WriteString("a=mid:0\r\n")
	b.WriteString("a=sctp-port:5000\r\n")
	b.WriteString("a=max-message-size:262144\r\n")
	for _, c := range p.C {
		fmt.Fprintf(&b, "a=%s\r\n", c)
	}
	return b.String()
}

func newPeerConnection() (*webrtc.PeerConnection, error) {
	return webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{stunURL}}},
	})
}

// gatherCandidates registers the callback and returns once ICE gathering is done.
func gatherCandidates(pc *webrtc.PeerConnection, out *[]string) <-chan struct{} {
	done := make(chan struct{})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			close(done)
			return
		}
		s := c.ToJSON().Candidate
		if s != "" {
			*out = append(*out, s)
		}
	})
	return done
}
