package account

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"

	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// params are the parameters of an argon2id hash.
type params struct {
	// memory is in KiB.
	memory  uint32
	time    uint32
	threads uint8
	saltLen uint32
	keyLen  uint32
}

// currentParams are the parameters that passwords are hashed with (PRD
// NFR-SEC-03): the second of the settings that RFC 9106 §4 recommends, for
// a server that cannot spare 2 GiB per hash. A hash made with others is
// replaced at the account's next sign-in (Service.Authenticate).
var currentParams = params{memory: 64 * 1024, time: 3, threads: 4, saltLen: 16, keyLen: 32}

// Bounds of the parameters that parseHash accepts, so that a stored hash that
// is not one the server made cannot make it allocate or compute without
// limit.
const (
	maxMemory  = 4 << 20 // 4 GiB, in KiB
	maxTime    = 100
	minSaltLen = 8
	minKeyLen  = 16
	maxBytes   = 1024
)

// errMalformedHash is returned by Verify for a stored hash that is not an
// argon2id hash in the PHC string format.
var errMalformedHash = errors.New("account: the stored password hash is not an argon2id hash")

// Hasher hashes passwords with argon2id, and checks them against their
// hashes. Each hash takes its parameters' memory, 64 MiB, so a Hasher runs
// only a few at a time, which bounds the memory that a burst of sign-ups or
// sign-ins can take: as many as the process has CPUs for, each hash using
// as many threads as its parameters' parallelism. The others wait.
type Hasher struct {
	params params
	// slots holds a token for each hash in progress.
	slots chan struct{}
}

// NewHasher returns a Hasher with the current parameters.
func NewHasher() *Hasher {
	return newHasher(currentParams, max(1, runtime.GOMAXPROCS(0)/int(currentParams.threads)))
}

func newHasher(p params, concurrent int) *Hasher {
	return &Hasher{params: p, slots: make(chan struct{}, concurrent)}
}

// derive computes the key of password with salt and p, once it has a slot.
// It returns ctx's error if ctx ends while it waits for one.
func (h *Hasher) derive(ctx context.Context, password secret.Value, salt []byte, p params, keyLen uint32) ([]byte,
	error) {
	select {
	case h.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.slots }()
	return argon2.IDKey(normalize(password), salt, p.time, p.memory, p.threads, keyLen), nil
}

// normalize returns the password as it is hashed: in Unicode normalization
// form NFKC, as NIST SP 800-63B §5.1.1.2 recommends, so that a password
// typed on another keyboard or system, which may compose its characters
// differently, still matches.
func normalize(password secret.Value) []byte {
	return []byte(norm.NFKC.String(password.Reveal()))
}

// Hash returns the hash of password with a new random salt, in the PHC
// string format, as in
//
//	$argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>
//
// with the salt and the key in unpadded base64. It returns ctx's error if
// ctx ends while it waits for a slot.
func (h *Hasher) Hash(ctx context.Context, password secret.Value) (string, error) {
	salt := make([]byte, h.params.saltLen)
	_, _ = rand.Read(salt)
	key, err := h.derive(ctx, password, salt, h.params, h.params.keyLen)
	if err != nil {
		return "", err
	}
	return encodeHash(h.params, salt, key), nil
}

// decoy returns a hash with the Hasher's parameters and a random salt and
// key, which takes as long to check a password against as a hash that Hash
// made, but which a password matches only by a chance of one in 2^256. It
// hashes nothing, so it costs nothing to make.
func (h *Hasher) decoy() string {
	salt, key := make([]byte, h.params.saltLen), make([]byte, h.params.keyLen)
	_, _ = rand.Read(salt)
	_, _ = rand.Read(key)
	return encodeHash(h.params, salt, key)
}

// Verify reports whether password matches encoded, a hash that Hash
// returned, and, if it does, whether the hash was made with other
// parameters than the Hasher's, so that it should be made again. It returns
// an error if encoded is not an argon2id hash, or if ctx ends while it waits
// for a slot.
func (h *Hasher) Verify(ctx context.Context, encoded string, password secret.Value) (match, rehash bool,
	err error) {
	p, salt, key, err := parseHash(encoded)
	if err != nil {
		return false, false, err
	}
	got, err := h.derive(ctx, password, salt, p, uint32(len(key)))
	if err != nil {
		return false, false, err
	}
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	return true, p != h.params, nil
}

var b64 = base64.RawStdEncoding

func encodeHash(p params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.memory, p.time, p.threads,
		b64.EncodeToString(salt), b64.EncodeToString(key))
}

// parseHash returns the parameters, the salt, and the key of an argon2id hash
// in the PHC string format, as encodeHash writes it. The parameters' saltLen
// and keyLen are those of the salt and the key.
func parseHash(encoded string) (params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" ||
		parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return params{}, nil, nil, errMalformedHash
	}
	var memory, time, threads uint64
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return params{}, nil, nil, errMalformedHash
	}
	for i, f := range []struct {
		name string
		v    *uint64
		max  uint64
	}{{"m", &memory, maxMemory}, {"t", &time, maxTime}, {"p", &threads, 255}} {
		v, ok := strings.CutPrefix(fields[i], f.name+"=")
		n, err := strconv.ParseUint(v, 10, 32)
		// A number is written without a sign or leading zeros.
		if !ok || err != nil || n == 0 || n > f.max || strconv.FormatUint(n, 10) != v {
			return params{}, nil, nil, errMalformedHash
		}
		*f.v = n
	}
	if memory < 8*threads {
		// RFC 9106 §3.1: at least 8 KiB per lane.
		return params{}, nil, nil, errMalformedHash
	}
	salt, err := b64.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < minSaltLen || len(salt) > maxBytes {
		return params{}, nil, nil, errMalformedHash
	}
	key, err := b64.Strict().DecodeString(parts[5])
	if err != nil || len(key) < minKeyLen || len(key) > maxBytes {
		return params{}, nil, nil, errMalformedHash
	}
	p := params{memory: uint32(memory), time: uint32(time), threads: uint8(threads), saltLen: uint32(len(salt)),
		keyLen: uint32(len(key))}
	return p, salt, key, nil
}
