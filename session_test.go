package main

import (
	"strings"
	"testing"
)

func TestSessionCodeRoundTrip(t *testing.T) {
	p := sdpPayload{
		U: "EsRY", P: "PnhrsDaRC2cccUcMR6rDVB",
		F: "32ea9c1e7b3f4f5b0f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70",
		S: "actpass",
		C: []string{"candidate:842163049 1 udp 1677729535 192.168.1.10 52373 typ host"},
	}
	code, err := encodeCode("sakura-momiji-42", p)
	if err != nil {
		t.Fatal(err)
	}
	name, q, err := decodeCode("junk before " + code + " junk after\n")
	if err != nil {
		t.Fatal(err)
	}
	if name != "sakura-momiji-42" {
		t.Fatalf("name = %q", name)
	}
	if q.U != p.U || q.P != p.P || q.F != p.F || q.S != p.S || len(q.C) != 1 || q.C[0] != p.C[0] {
		t.Fatalf("payload mismatch: %+v", q)
	}
}

func TestSDPBuildExtract(t *testing.T) {
	p := sdpPayload{
		U: "EsRY", P: "PnhrsDaRC2cccUcMR6rDVB",
		F: "32ea9c1e7b3f4f5b0f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70",
		S: "active",
		C: []string{"candidate:842163049 1 udp 1677729535 192.168.1.10 52373 typ host"},
	}
	sdp := buildSDP(p)
	u, pw, fp, s, err := extractSDP(sdp)
	if err != nil {
		t.Fatal(err)
	}
	if u != p.U || pw != p.P || fp != p.F || s != p.S {
		t.Fatalf("round trip mismatch: %q %q %q %q", u, pw, fp, s)
	}
	if !strings.Contains(sdp, "a=fingerprint:sha-256 32:ea:9c:1e:") {
		t.Fatal("fingerprint not colonized")
	}
}

func TestGenerateSessionName(t *testing.T) {
	for i := 0; i < 100; i++ {
		n := generateSessionName()
		parts := strings.Split(n, "-")
		if len(parts) < 3 {
			t.Fatalf("name %q too short", n)
		}
	}
}
