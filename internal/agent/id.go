package agent

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// Crockford base32, as used by ULID: no I, L, O, U to avoid transcription errors.
const enc = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	idMu     sync.Mutex
	lastMS   int64
	lastRand [10]byte
)

// newID returns a ULID: 48-bit millisecond timestamp + 80 bits of randomness,
// encoded as 26 characters. Lexicographic sort order matches time order, which
// is what makes event streams cheap to page through and resume.
func newID() string {
	idMu.Lock()
	defer idMu.Unlock()

	ms := time.Now().UTC().UnixMilli()
	if ms == lastMS {
		// Same millisecond: increment the random component so IDs stay
		// strictly increasing rather than colliding.
		for i := 9; i >= 0; i-- {
			lastRand[i]++
			if lastRand[i] != 0 {
				break
			}
		}
	} else {
		lastMS = ms
		if _, err := rand.Read(lastRand[:]); err != nil {
			// Fall back to time-derived bytes; IDs stay unique via the
			// increment path above even if the entropy source fails.
			binary.BigEndian.PutUint64(lastRand[:8], uint64(ms))
		}
	}

	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	copy(b[6:], lastRand[:])

	out := make([]byte, 26)
	out[0] = enc[(b[0]&224)>>5]
	out[1] = enc[b[0]&31]
	for i, shift := 2, 0; i < 26; i++ {
		// Read 5 bits at a time from the 128-bit value, MSB first.
		bitPos := 10 + shift
		byteIdx := bitPos / 8
		bitOff := bitPos % 8
		var v uint16
		v = uint16(b[byteIdx]) << 8
		if byteIdx+1 < 16 {
			v |= uint16(b[byteIdx+1])
		}
		out[i] = enc[(v>>(11-bitOff))&31]
		shift += 5
	}
	return string(out)
}
