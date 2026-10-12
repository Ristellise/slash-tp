package main

import (
	"encoding/binary"

	"github.com/kalafut/imohash"
)

const (
	frameAnnounce = 0
	frameChunk    = 1
	frameVerdict  = 2

	hashLen = 16

	chunkSize    = 60 * 1024
	bufThreshold = 4 << 20

	sendWindow = 16
)

type ctrlMsg struct {
	Type string `json:"type"`
}

// announce: [type][fileID 4][size 8][mode 4][digest 16][nameLen 2][name]
func announceFrame(id uint32, name string, size int64, mode uint32, digest [hashLen]byte) []byte {
	n := []byte(name)
	frame := make([]byte, 35+len(n))
	frame[0] = frameAnnounce
	binary.BigEndian.PutUint32(frame[1:5], id)
	binary.BigEndian.PutUint64(frame[5:13], uint64(size))
	binary.BigEndian.PutUint32(frame[13:17], mode)
	copy(frame[17:33], digest[:])
	binary.BigEndian.PutUint16(frame[33:35], uint16(len(n)))
	copy(frame[35:], n)
	return frame
}

// chunk: [type][fileID 4][offset 8][payload]
func chunkFrame(id uint32, off int64, payload []byte) []byte {
	frame := make([]byte, 13+len(payload))
	frame[0] = frameChunk
	binary.BigEndian.PutUint32(frame[1:5], id)
	binary.BigEndian.PutUint64(frame[5:13], uint64(off))
	copy(frame[13:], payload)
	return frame
}

// verdict: [type][fileID 4][have 1]
func verdictFrame(id uint32, have bool) []byte {
	frame := make([]byte, 6)
	frame[0] = frameVerdict
	binary.BigEndian.PutUint32(frame[1:5], id)
	if have {
		frame[5] = 1
	}
	return frame
}

func fileDigest(path string) ([hashLen]byte, error) {
	d, err := imohash.SumFile(path)
	var digest [hashLen]byte
	copy(digest[:], d[:])
	return digest, err
}
