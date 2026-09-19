// Package xid generates the public resource identifiers used by the API.
//
// Public IDs carry a type prefix (agent_, sess_, sevt_, ...) followed by a
// ULID-like time-ordered identifier, except environment IDs which follow the
// snapshot contract's UUID form (env_019e8f00-...).
package xid

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	lastMS  int64
	lastSeq uint16
)

// ulid returns a 26-character Crockford-base32 string (ULID shape) that is
// lexicographically sortable by generation time.
func ulid(t time.Time) string {
	ms := t.UnixMilli()
	mu.Lock()
	if ms == lastMS {
		lastSeq++
		if lastSeq >= 3200 { // entropy budget per millisecond
			ms++
			// Advance the bucket: leaving lastMS behind would reissue
			// earlier sequence numbers and move IDs backwards in time.
			lastMS = ms
			lastSeq = 0
		}
	} else if ms > lastMS {
		lastMS = ms
		lastSeq = 0
	} else {
		ms = lastMS // clock went backwards; reuse last bucket
	}
	seq := lastSeq
	mu.Unlock()

	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	binary.BigEndian.PutUint16(b[6:8], seq)
	rand.Read(b[8:16])
	return encodeULID(b)
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func encodeULID(b [16]byte) string {
	var out [26]byte
	// Two leading zero bits plus the 128 payload bits fill 26 Crockford
	// chars exactly, so no bit of the random suffix is dropped. The old tail
	// encoding silently discarded the final bit (63 of 64 random bits).
	n := 0
	var acc uint64
	var accBits uint = 2
	for i := 0; i < 16 && n < 26; i++ {
		acc = acc<<8 | uint64(b[i])
		accBits += 8
		for accBits >= 5 && n < 26 {
			out[n] = crockford[(acc>>(accBits-5))&0x1f]
			accBits -= 5
			n++
		}
	}
	return string(out[:])
}

// uuidV7 produces a time-ordered UUID formatted in canonical hyphenated form.
func uuidV7(t time.Time) string {
	ms := uint64(t.UnixMilli())
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	rand.Read(b[6:16])      // one syscall for the whole random part
	b[6] = 0x70 | b[6]&0x0f // version 7
	b[8] = 0x80 | b[8]&0x3f // RFC 4122 variant
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}

// New returns a prefixed public identifier such as "sess_01J...".
func New(prefix string) string { return prefix + "_" + ulid(time.Now()) }

// NewEnv returns the environment identifier form mandated by the contract
// (env_ + UUID v7), which clients validate with a UUID pattern.
func NewEnv() string { return "env_" + uuidV7(time.Now()) }

// SkillVersion returns the server-generated epoch-microsecond version string.
func SkillVersion(t time.Time) string {
	us := t.UnixMicro()
	if us < 0 {
		us = 0
	}
	if us > math.MaxInt64 {
		us = math.MaxInt64
	}
	return fmt.Sprintf("%d", us)
}

// HasPrefix reports whether id carries the expected type prefix.
func HasPrefix(id, prefix string) bool { return strings.HasPrefix(id, prefix+"_") }
