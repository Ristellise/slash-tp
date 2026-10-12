package main

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
)

const (
	codeVersion = "tp1"
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

type iceState struct {
	mu sync.Mutex
	s  string
}

func (i *iceState) set(s webrtc.ICEConnectionState) {
	i.mu.Lock()
	i.s = s.String()
	i.mu.Unlock()
	fmt.Fprintln(os.Stderr, "tp: ice:", s)
}

func (i *iceState) get() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.s
}

func reportConnFailure(lastICE string, local, remote []iceCand) {
	fmt.Fprintln(os.Stderr, "tp: connection failed — diagnostics:")
	fmt.Fprintf(os.Stderr, "  last ice state: %s\n", lastICE)
	fmt.Fprintln(os.Stderr, "  local candidates:")
	for _, c := range local {
		fmt.Fprintf(os.Stderr, "    %-5s %s:%d\n", candTypeNames[c.typ], c.ip, c.port)
	}
	fmt.Fprintln(os.Stderr, "  remote candidates:")
	for _, c := range remote {
		fmt.Fprintf(os.Stderr, "    %-5s %s:%d\n", candTypeNames[c.typ], c.ip, c.port)
	}
	var localSrflx, remoteSrflx bool
	for _, c := range local {
		if c.typ == candSrflx {
			localSrflx = true
		}
	}
	for _, c := range remote {
		if c.typ == candSrflx {
			remoteSrflx = true
		}
	}
	fmt.Fprintln(os.Stderr, "  likely cause:")
	switch {
	case len(remote) == 0:
		fmt.Fprintln(os.Stderr, "    no remote candidates in the pasted code — it may be truncated or from an incompatible tp")
	case !localSrflx || !remoteSrflx:
		fmt.Fprintln(os.Stderr, "    a peer has no srflx candidate (STUN unreachable) — direct connection only works on the same LAN")
	default:
		fmt.Fprintln(os.Stderr, "    symmetric NAT on either side — tp is P2P-only (no TURN relay); try the same LAN or a port-forward")
	}
}

type errLogger struct{ scope string }

func (l errLogger) Trace(msg string)                  {}
func (l errLogger) Tracef(format string, args ...any) {}
func (l errLogger) Debug(msg string)                  {}
func (l errLogger) Debugf(format string, args ...any) {}
func (l errLogger) Info(msg string)                   {}
func (l errLogger) Infof(format string, args ...any)  {}
func (l errLogger) Warn(msg string) {
	fmt.Fprintln(os.Stderr, "tp: ["+l.scope+"]", msg)
}
func (l errLogger) Warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tp: ["+l.scope+"] "+format+"\n", args...)
}
func (l errLogger) Error(msg string) {
	fmt.Fprintln(os.Stderr, "tp: ["+l.scope+"]", msg)
}
func (l errLogger) Errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tp: ["+l.scope+"] "+format+"\n", args...)
}

type loggerFactory struct{}

func (loggerFactory) NewLogger(scope string) logging.LeveledLogger { return errLogger{scope} }
