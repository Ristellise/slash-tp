package main

import (
	"strings"
	"testing"
)

func TestB32RoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 15, 16, 17, 31, 32, 33, 100} {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i * 7)
		}
		got, err := b32Decode(b32Encode(b))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(b) {
			t.Fatalf("round trip failed for %d bytes", n)
		}
	}
}

func TestB32Tolerant(t *testing.T) {
	b := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02}
	mangled := strings.ToUpper(b32Encode(b))
	mangled = strings.ReplaceAll(mangled, "1", "l")
	mangled = strings.ReplaceAll(mangled, "", "-")
	if got, err := b32Decode(mangled); err != nil || string(got) != string(b) {
		t.Fatalf("tolerant decode failed: %v %v", got, err)
	}
	if _, err := b32Decode("tp2#~!?"); err == nil {
		t.Fatal("expected error for bad characters")
	}
}

func TestCandRoundTrip(t *testing.T) {
	c, ok := parseCand("candidate:842163049 1 udp 1677729535 192.168.1.10 52373 typ host")
	if !ok || c.typ != candHost || c.prio != 1677729535 || c.ip.String() != "192.168.1.10" || c.port != 52373 {
		t.Fatalf("parse failed: %+v", c)
	}
	if c2, ok := parseCand(c.marshal()); !ok || c2.typ != c.typ || c2.prio != c.prio || c2.ip.String() != c.ip.String() || c2.port != c.port {
		t.Fatalf("re-parse failed: %q", c.marshal())
	}
	s, ok := parseCand("candidate:2588556477 1 udp 1694498815 203.0.113.7 49170 typ srflx raddr 192.168.1.10 rport 52373")
	if !ok || s.typ != candSrflx || s.raddr.String() != "192.168.1.10" || s.rport != 52373 {
		t.Fatalf("srflx parse failed: %+v", s)
	}
	if _, ok := parseCand("candidate:2588556477 1 tcp 1694498815 203.0.113.7 49170 typ srflx"); ok {
		t.Fatal("tcp candidate should be rejected")
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	s, err := newSession()
	if err != nil {
		t.Fatal(err)
	}
	cands := []iceCand{
		{typ: candHost, prio: 1677729535, ip: []byte{192, 168, 1, 10}, port: 52373},
		{typ: candSrflx, prio: 1694498815, ip: []byte{203, 0, 113, 7}, port: 49170,
			raddr: []byte{192, 168, 1, 10}, rport: 52373},
	}
	code := encodeCode(encodePayload(s.Seed, [][]iceCand{cands, cands}))
	if len(code) > 240 {
		t.Fatalf("code too long: %d", len(code))
	}
	seed, pcs, err := decodeCode("noise " + code + " noise")
	if err != nil {
		t.Fatal(err)
	}
	if string(seed) != string(s.Seed) {
		t.Fatal("seed mismatch")
	}
	if len(pcs) != 2 {
		t.Fatalf("pc count mismatch: %d", len(pcs))
	}
	got := pcs[0]
	if len(got) != 2 || got[0].typ != candHost || got[1].typ != candSrflx || got[1].rport != 52373 {
		t.Fatalf("cands mismatch: %+v", got)
	}

	ansCode := encodeCode(encodePayload(nil, [][]iceCand{cands}))
	seed2, got2, err := decodeCode(ansCode)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed2) != 0 || len(got2) != 1 || len(got2[0]) != 2 {
		t.Fatalf("answer payload mismatch: %d %d", len(seed2), len(got2))
	}
}

func TestSessionDerivation(t *testing.T) {
	s1, err := resumeSession([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := resumeSession([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if s1.Name != s2.Name || s1.Fp != s2.Fp {
		t.Fatal("derivation is not deterministic")
	}
	u1, p1 := s1.creds(0)
	u2, p2 := s2.creds(0)
	u3, p3 := s1.creds(1)
	if u1 != u2 || p1 != p2 || u1 == u3 || p1 == p3 {
		t.Fatal("per-lane credentials wrong")
	}
	if len(u1) < 4 || len(p1) < 22 {
		t.Fatalf("ICE credentials too short: %d %d", len(u1), len(p1))
	}
	if !strings.Contains(s1.Fp, ":") {
		t.Fatalf("fingerprint not colonized: %q", s1.Fp)
	}
	parts := strings.Split(s1.Name, "-")
	if len(parts) < 3 {
		t.Fatalf("name %q too short", s1.Name)
	}
}
