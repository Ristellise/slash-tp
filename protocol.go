package main

import "encoding/binary"

const (
	chunkSize    = 60 * 1024 // stays under the 64 KiB SCTP message limit
	frameHeader  = 12
	bufThreshold = 4 << 20 // per-channel buffered amount pause threshold
)

type fileEntry struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mode uint32 `json:"mode"`
}

type ctrlMsg struct {
	Type string `json:"type"`
}

type manifestMsg struct {
	Type  string      `json:"type"`
	Top   string      `json:"top"`
	Chunk int         `json:"chunk"`
	Files []fileEntry `json:"files"`
}

func framePut(buf []byte, id uint32, off int64) {
	binary.BigEndian.PutUint32(buf[0:4], id)
	binary.BigEndian.PutUint64(buf[4:12], uint64(off))
}

func frameGet(buf []byte) (id uint32, off int64) {
	return binary.BigEndian.Uint32(buf[0:4]), int64(binary.BigEndian.Uint64(buf[4:12]))
}
