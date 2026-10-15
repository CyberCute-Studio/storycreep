package database

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/text/secure/precis"
)

// scramIterations is PostgreSQL's default iteration count for SCRAM
// verifiers, which the server's verifiers use where the database server has
// no scram_iterations setting, as before PostgreSQL 16.
const scramIterations = 4096

// scramVerifier returns the SCRAM-SHA-256 verifier that PostgreSQL stores for
// password (RFC 5802, RFC 7677), with a new random salt and the given
// iteration count. CREATE ROLE and ALTER ROLE accept a verifier in place of
// the password, so the password itself is never sent to the server, which
// could log the statement.
func scramVerifier(password string, iterations int) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return scramVerifierWith(password, salt, iterations)
}

func scramVerifierWith(password string, salt []byte, iterations int) (string, error) {
	storedKey, serverKey, err := scramKeys(password, salt, iterations)
	if err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, b64(salt), b64(storedKey), b64(serverKey)), nil
}

func scramKeys(password string, salt []byte, iterations int) (storedKey, serverKey []byte, err error) {
	salted, err := pbkdf2.Key(sha256.New, normalizePassword(password), salt, iterations, sha256.Size)
	if err != nil {
		return nil, nil, err
	}
	clientKey := hmacSHA256(salted, "Client Key")
	sum := sha256.Sum256(clientKey)
	return sum[:], hmacSHA256(salted, "Server Key"), nil
}

// normalizePassword prepares a password the way pgx, the server's client,
// does before it hashes it for SCRAM: with the PRECIS OpaqueString profile
// (RFC 8265), or unchanged if the profile rejects it. A verifier made from
// the password differently would not match what the server's connections
// send. pgx uses the profile in place of SASLprep (RFC 4013), which libpq
// and PostgreSQL use. The two agree on most passwords, but not on all:
// SASLprep applies NFKC normalization where the profile applies NFC, so
// they differ on compatibility characters such as the ligature "ﬁ".
func normalizePassword(password string) string {
	if p, err := precis.OpaqueString.String(password); err == nil {
		return p
	}
	return password
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// scramMatches reports whether stored, a role's password as pg_authid holds
// it, is a SCRAM-SHA-256 verifier of password. It is false for any other
// stored form, such as an MD5 hash or no password.
func scramMatches(stored, password string) bool {
	method, rest, ok := strings.Cut(stored, "$")
	if !ok || method != "SCRAM-SHA-256" {
		return false
	}
	params, keys, ok := strings.Cut(rest, "$")
	if !ok {
		return false
	}
	iterText, saltText, ok1 := strings.Cut(params, ":")
	storedText, serverText, ok2 := strings.Cut(keys, ":")
	if !ok1 || !ok2 {
		return false
	}
	iterations, err := strconv.Atoi(iterText)
	if err != nil || iterations < 1 {
		return false
	}
	b64 := base64.StdEncoding.DecodeString
	salt, err1 := b64(saltText)
	wantStored, err2 := b64(storedText)
	wantServer, err3 := b64(serverText)
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	gotStored, gotServer, err := scramKeys(password, salt, iterations)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(gotStored, wantStored) == 1 && subtle.ConstantTimeCompare(gotServer, wantServer) == 1
}
