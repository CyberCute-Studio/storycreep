package ids

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// UUID is a 128-bit identifier in big-endian byte order: an entity's primary
// key. It deliberately has no JSON or text encoding, because an identifier
// that leaves the server must carry its type prefix; use [Encode].
type UUID [16]byte

// New returns a new UUIDv7 (RFC 9562 §5.7) for the current time: a 48-bit Unix
// timestamp in milliseconds followed by 74 random bits. UUIDs made in
// different milliseconds sort by time; within one millisecond their order is
// random.
func New() UUID {
	var r [10]byte
	// crypto/rand.Read never returns an error: if the system's random source
	// fails, the program crashes rather than produce guessable identifiers.
	_, _ = rand.Read(r[:])
	return newV7(time.Now().UnixMilli(), r)
}

// newV7 builds a UUIDv7 from a Unix time in milliseconds, of which it keeps
// the low 48 bits, and 10 random bytes. The version and variant bits replace
// the top 4 bits of r[0] and the top 2 bits of r[2].
func newV7(ms int64, r [10]byte) UUID {
	var u UUID
	t := uint64(ms)
	for i := range 6 {
		u[i] = byte(t >> (40 - 8*i))
	}
	copy(u[6:], r[:])
	u[6] = 0x70 | u[6]&0x0F
	u[8] = 0x80 | u[8]&0x3F
	return u
}

// String returns u in the lowercase hyphenated form of RFC 9562, such as
// "017f22e2-79b0-7cc3-98c4-dc0c0c07398f".
func (u UUID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}

// ErrUUIDSyntax is returned by [ParseUUID] for text that is not a hyphenated
// UUID.
var ErrUUIDSyntax = errors.New("ids: not a UUID in the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")

// ParseUUID parses a UUID in the hyphenated form of RFC 9562. The hexadecimal
// digits are case-insensitive. Client-generated values without a type prefix,
// such as command IDs, use this form.
func ParseUUID(s string) (UUID, error) {
	var u UUID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return UUID{}, ErrUUIDSyntax
	}
	src := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	if _, err := hex.Decode(u[:], []byte(src)); err != nil {
		return UUID{}, ErrUUIDSyntax
	}
	return u, nil
}
