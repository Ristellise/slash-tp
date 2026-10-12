package main

import "encoding/binary"

const (
	frameAnnounce = 0
	frameChunk    = 1

	chunkSize    = 60 * 1024
	bufThreshold = 4 << 20
)

type ctrlMsg struct {
	Type string `json:"type"`
}

// announce: [type][fileID 4][size 8][mode 4][nameLen 2][name]
func announceFrame(id uint32, name string, size int64, mode uint32) []byte {
	n := []byte(name)
	frame := make([]byte, 19+len(n))
	frame[0] = frameAnnounce
	binary.BigEndian.PutUint32(frame[1:5], id)
	binary.BigEndian.PutUint64(frame[5:13], uint64(size))
	binary.BigEndian.PutUint32(frame[13:17], mode)
	binary.BigEndian.PutUint16(frame[17:19], uint16(len(n)))
	copy(frame[19:], n)
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
