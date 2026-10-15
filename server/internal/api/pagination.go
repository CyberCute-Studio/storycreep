package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
)

// The limits of a page (PRD FR-API-04).
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// maxCursorLength bounds the cursors DecodeCursor reads. The cursors the
// API writes hold a few keys, far less.
const maxCursorLength = 1024

// PageParams are the query parameters of a list: ?cursor=&limit= (PRD
// FR-API-04). Embed them in an operation's input.
//
// Lists use keyset pagination, which stays stable while rows are added and
// removed between pages (PRD FR-API-04 AC2): a page's cursor holds the key of
// the page's last row, and the next page is the rows whose keys sort after
// it. A list sorts by a key that is unique for every row, such as
// (created_at, id), and its query reads, for a cursor holding the key k,
//
//	SELECT … WHERE (created_at, id) > (k.Created, k.ID)
//	ORDER BY created_at, id LIMIT p.Fetch()
//
// with < and DESC for the opposite order, and no WHERE for the first page.
// A row added later falls into whichever page its key sorts into, and no row
// is skipped or repeated, as an OFFSET would skip or repeat them. The key is a
// struct whose fields encode as JSON; the cursor is that JSON in base64url,
// which clients treat as opaque.
type PageParams struct {
	Cursor string `query:"cursor" maxLength:"1024" doc:"The next_cursor of the previous page; empty for the first page"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"How many items the page holds at most"`
}

// Fetch returns how many rows a page query reads: one more than the page
// holds, which tells NewPage whether another page follows.
func (p PageParams) Fetch() int {
	return p.limit() + 1
}

func (p PageParams) limit() int {
	if p.Limit <= 0 {
		return DefaultPageLimit
	}
	return min(p.Limit, MaxPageLimit)
}

// Page is one page of a list.
type Page[T any] struct {
	Items []T `json:"items" nullable:"false"`
	// NextCursor is null on the last page.
	NextCursor *string `json:"next_cursor" doc:"The cursor of the next page, or null if this is the last page"`
}

// NewPage returns the page of rows, which a query read with p.Fetch() as its
// limit, and the cursor of the next page if one follows: the key of the
// page's last row, as key returns it.
func NewPage[T, K any](p PageParams, rows []T, key func(T) K) (Page[T], error) {
	page := Page[T]{Items: rows}
	if page.Items == nil {
		page.Items = []T{}
	}
	if n := p.limit(); len(rows) > n {
		page.Items = rows[:n]
		cursor, err := EncodeCursor(key(rows[n-1]))
		if err != nil {
			return Page[T]{}, err
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

// EncodeCursor returns the cursor that holds the key k.
func EncodeCursor[K any](k K) (string, error) {
	b, err := json.Marshal(k)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor returns the key that the cursor s holds, which EncodeCursor
// wrote for a K. Any other s is a client's mistake, answered with
// invalid_cursor.
func DecodeCursor[K any](s string) (K, error) {
	var k K
	invalid := func() (K, error) {
		var zero K
		return zero, NewError(CodeInvalidCursor)
	}
	if len(s) > maxCursorLength {
		return invalid()
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return invalid()
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&k); err != nil {
		return invalid()
	}
	if _, err := d.Token(); err != io.EOF {
		return invalid()
	}
	return k, nil
}
