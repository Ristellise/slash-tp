package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"regexp"
	"strconv"
	"strings"
)

const (
	candHost  = 0
	candSrflx = 1
	candPrflx = 2
	candRelay = 3
)

type iceCand struct {
	typ   byte
	prio  uint32
	ip    net.IP
	port  uint16
	raddr net.IP
	rport uint16
}

var candTypes = map[string]byte{"host": candHost, "srflx": candSrflx, "prflx": candPrflx, "relay": candRelay}

var candTypeNames = map[byte]string{candHost: "host", candSrflx: "srflx", candPrflx: "prflx", candRelay: "relay"}

func parseCand(s string) (iceCand, bool) {
	var c iceCand
	f := strings.Fields(strings.TrimPrefix(s, "candidate:"))
	if len(f) < 8 || f[2] != "udp" {
		return c, false
	}
	typ, ok := candTypes[f[7]]
	if !ok {
		return c, false
	}
	prio, err := strconv.ParseUint(f[3], 10, 32)
	if err != nil {
		return c, false
	}
	port, err := strconv.ParseUint(f[5], 10, 16)
	if err != nil {
		return c, false
	}
	c = iceCand{typ: typ, prio: uint32(prio), ip: net.ParseIP(f[4]), port: uint16(port)}
	if c.ip == nil {
		return c, false
	}
	for i := 8; i+1 < len(f); i += 2 {
		switch f[i] {
		case "raddr":
			c.raddr = net.ParseIP(f[i+1])
			if c.raddr == nil {
				return iceCand{}, false
			}
		case "rport":
			p, err := strconv.ParseUint(f[i+1], 10, 16)
			if err != nil {
				return iceCand{}, false
			}
			c.rport = uint16(p)
		}
	}
	return c, true
}

func (c iceCand) marshal() string {
	h := fnv.New32a()
	h.Write([]byte{c.typ})
	h.Write(c.ip)
	found := h.Sum32()
	typ := candTypeNames[c.typ]
	line := fmt.Sprintf("candidate:%d 1 udp %d %s %d typ %s", found, c.prio, c.ip, c.port, typ)
	if c.typ == candSrflx || c.typ == candPrflx {
		line += fmt.Sprintf(" raddr %s rport %d", c.raddr, c.rport)
	}
	return line
}

// offerPayload: [ver|flags][seed 16][pcs][ (count)[cands] ] per pc
// answerPayload: [ver|flags][ (count)[cands] ] per pc
// cand: [type|fam][prio 4][ip][port 2][raddr][rport 2]
func encodePayload(seed []byte, pcs [][]iceCand) []byte {
	var b []byte
	flags := byte(0)
	if len(seed) > 0 {
		flags |= 1
	}
	b = append(b, 0x30|flags)
	if len(seed) > 0 {
		b = append(b, seed...)
	}
	b = append(b, byte(len(pcs)))
	for _, cands := range pcs {
		b = append(b, byte(len(cands)))
		for _, c := range cands {
			fam := byte(0)
			ipv6 := c.ip.To4() == nil
			if ipv6 {
				fam = 4
			}
			b = append(b, c.typ|fam)
			b = binary.BigEndian.AppendUint32(b, c.prio)
			b = appendIP(b, c.ip)
			b = binary.BigEndian.AppendUint16(b, c.port)
			if c.typ == candSrflx || c.typ == candPrflx {
				if c.raddr == nil {
					c.raddr = net.IPv4zero
				}
				b = appendIP(b, c.raddr)
				b = binary.BigEndian.AppendUint16(b, c.rport)
			}
		}
	}
	return b
}

func appendIP(b []byte, ip net.IP) []byte {
	if v4 := ip.To4(); v4 != nil {
		return append(b, v4...)
	}
	return append(b, ip.To16()...)
}

func decodePayload(b []byte) (seed []byte, pcs [][]iceCand, err error) {
	if len(b) < 2 || b[0]>>4 != 3 {
		return nil, nil, errors.New("unsupported session code version")
	}
	p := b[1:]
	if b[0]&1 != 0 {
		if len(p) < seedLen+1 {
			return nil, nil, errors.New("truncated seed")
		}
		seed = p[:seedLen]
		p = p[seedLen:]
	}
	if len(p) < 1 {
		return nil, nil, errors.New("truncated pc count")
	}
	npcs := int(p[0])
	p = p[1:]
	for j := 0; j < npcs; j++ {
		if len(p) < 1 {
			return nil, nil, errors.New("truncated candidate count")
		}
		n := int(p[0])
		p = p[1:]
		cands := []iceCand{}
		for i := 0; i < n; i++ {
			if len(p) < 1 {
				return nil, nil, errors.New("truncated candidate")
			}
			hdr := p[0]
			ipv6 := hdr&4 != 0
			ipLen := 4
			if ipv6 {
				ipLen = 16
			}
			need := 1 + 4 + ipLen + 2
			if hdr&3 == candSrflx || hdr&3 == candPrflx {
				need += ipLen + 2
			}
			if len(p) < need {
				return nil, nil, errors.New("truncated candidate")
			}
			c := iceCand{typ: hdr & 3, prio: binary.BigEndian.Uint32(p[1:5])}
			p = p[5:]
			if ipv6 {
				c.ip = net.IP(append([]byte(nil), p[:16]...))
			} else {
				c.ip = net.IPv4(p[0], p[1], p[2], p[3])
			}
			p = p[ipLen:]
			c.port = binary.BigEndian.Uint16(p)
			p = p[2:]
			if c.typ == candSrflx || c.typ == candPrflx {
				if ipv6 {
					c.raddr = net.IP(append([]byte(nil), p[:16]...))
				} else {
					c.raddr = net.IPv4(p[0], p[1], p[2], p[3])
				}
				p = p[ipLen:]
				c.rport = binary.BigEndian.Uint16(p)
				p = p[2:]
			}
			cands = append(cands, c)
		}
		pcs = append(pcs, cands)
	}
	if len(p) != 0 {
		return nil, nil, errors.New("trailing garbage in payload")
	}
	return seed, pcs, nil
}

func candStrings(cands []iceCand) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.marshal()
	}
	return out
}

const b32Alpha = "0123456789abcdefghjkmnpqrstvwxyz"

var b32Values = func() [256]byte {
	var v [256]byte
	for i := range v {
		v[i] = 255
	}
	for i := 0; i < len(b32Alpha); i++ {
		v[b32Alpha[i]] = byte(i)
	}
	return v
}()

func b32Encode(b []byte) string {
	var sb strings.Builder
	var buf, bits uint32
	for _, c := range b {
		buf = buf<<8 | uint32(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			sb.WriteByte(b32Alpha[(buf>>bits)&31])
		}
	}
	if bits > 0 {
		sb.WriteByte(b32Alpha[(buf<<(5-bits))&31])
	}
	return sb.String()
}

func b32Decode(s string) ([]byte, error) {
	var out []byte
	var buf, bits uint32
	for _, c := range s {
		switch c {
		case '-', ' ', '\t', '\r', '\n':
			continue
		case 'i', 'I', 'l', 'L':
			c = '1'
		case 'o', 'O':
			c = '0'
		}
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		if c < 0 || c > 127 {
			return nil, fmt.Errorf("bad character %q in code", string(c))
		}
		v := b32Values[c]
		if v == 255 {
			return nil, fmt.Errorf("bad character %q in code", string(c))
		}
		buf = buf<<5 | uint32(v)
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(buf>>bits))
		}
	}
	return out, nil
}

func encodeCode(payload []byte) string {
	s := b32Encode(payload)
	var sb strings.Builder
	for i := 0; i < len(s); i += 5 {
		if i > 0 {
			sb.WriteByte('-')
		}
		sb.WriteString(s[i:min(i+5, len(s))])
	}
	return codeVersion + "#" + sb.String()
}

var codeRe = regexp.MustCompile(codeVersion + `#[0-9a-zA-Z-]+`)

func decodeCode(code string) (seed []byte, pcs [][]iceCand, err error) {
	m := codeRe.FindString(code)
	if m == "" {
		return nil, nil, fmt.Errorf("no session code found (expected %s#...)", codeVersion)
	}
	raw, err := b32Decode(m[len(codeVersion)+1:])
	if err != nil {
		return nil, nil, err
	}
	return decodePayload(raw)
}
