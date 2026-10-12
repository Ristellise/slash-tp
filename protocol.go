package main

import (
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os"
)

const (
	frameAnnounce = 0
	frameChunk    = 1
	frameVerdict  = 2

	chunkSize    = 60 * 1024
	hashWindow   = chunkSize
	hashLen      = 16
	bufThreshold = 4 << 20

	sendWindow = 16
)

type ctrlMsg struct {
	Type string `json:"type"`
}

// announce: [type][fileID 4][size 8][mode 4][firstHash 16][lastHash 16][nameLen 2][name]
func announceFrame(id uint32, name string, size int64, mode uint32, first, last [hashLen]byte) []byte {
	n := []byte(name)
	frame := make([]byte, 51+len(n))
	frame[0] = frameAnnounce
	binary.BigEndian.PutUint32(frame[1:5], id)
	binary.BigEndian.PutUint64(frame[5:13], uint64(size))
	binary.BigEndian.PutUint32(frame[13:17], mode)
	copy(frame[17:33], first[:])
	copy(frame[33:49], last[:])
	binary.BigEndian.PutUint16(frame[49:51], uint16(len(n)))
	copy(frame[51:], n)
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

func edgeHashes(r io.ReaderAt, size int64) (first, last [hashLen]byte) {
	buf := make([]byte, min(int64(hashWindow), size))
	r.ReadAt(buf, 0)
	h := sha256.Sum256(buf)
	first = [hashLen]byte(h[:hashLen])
	r.ReadAt(buf, size-int64(len(buf)))
	h = sha256.Sum256(buf)
	last = [hashLen]byte(h[:hashLen])
	return first, last
}

func fileEdgeHashes(path string, size int64) (first, last [hashLen]byte, err error) {
	if size <= 0 {
		return first, last, nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return first, last, err
	}
	defer fh.Close()
	first, last = edgeHashes(fh, size)
	return first, last, nil
}
