package session

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

var (
	uuidMu        sync.Mutex
	lastTimestamp int64 = -1 << 62
	sequence      uint32
)

// UUIDv7 generates a time-ordered UUIDv7 with a monotonic per-process counter,
// matching pi's uuidv7.
func UUIDv7() string {
	var random [16]byte
	_, _ = rand.Read(random[:])
	ts := time.Now().UnixMilli()

	uuidMu.Lock()
	if ts > lastTimestamp {
		sequence = uint32(random[6])<<24 | uint32(random[7])<<16 | uint32(random[8])<<8 | uint32(random[9])
		lastTimestamp = ts
	} else {
		sequence++
		if sequence == 0 {
			lastTimestamp++
		}
	}
	t := lastTimestamp
	seq := sequence
	uuidMu.Unlock()

	var b [16]byte
	b[0] = byte(t >> 40)
	b[1] = byte(t >> 32)
	b[2] = byte(t >> 24)
	b[3] = byte(t >> 16)
	b[4] = byte(t >> 8)
	b[5] = byte(t)
	b[6] = 0x70 | byte((seq>>28)&0x0f)
	b[7] = byte(seq >> 20)
	b[8] = 0x80 | byte((seq>>14)&0x3f)
	b[9] = byte(seq >> 6)
	b[10] = byte((seq&0x3f)<<2) | (random[10] & 0x03)
	b[11] = random[11]
	b[12] = random[12]
	b[13] = random[13]
	b[14] = random[14]
	b[15] = random[15]

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
