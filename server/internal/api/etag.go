package api

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// ETag returns a strong entity tag for a representation: a quoted hash of
// what identifies its content, such as its encoded bytes or the revision it
// was read at (PRD §10.5). Two representations get the same tag exactly when
// their content is the same.
func ETag(content []byte) string {
	sum := sha256.Sum256(content)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
}

// IfNoneMatch reports whether the If-None-Match header value h matches the
// entity tag etag, by the weak comparison of RFC 9110 §13.1.2: then a GET or
// HEAD is answered with NotModified, because the client's copy is current.
func IfNoneMatch(h, etag string) bool {
	if strings.TrimSpace(h) == "*" {
		return true
	}
	want := strings.TrimPrefix(etag, "W/")
	for tag := range strings.SplitSeq(h, ",") {
		if strings.TrimPrefix(strings.TrimSpace(tag), "W/") == want {
			return true
		}
	}
	return false
}

// NotModified is what an operation returns to answer 304 Not Modified, with
// the entity tag in the ETag header and no body.
func NotModified(etag string) error {
	return huma.ErrorWithHeaders(huma.Status304NotModified(), http.Header{"ETag": {etag}})
}
