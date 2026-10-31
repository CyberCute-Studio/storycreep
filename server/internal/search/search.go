// Package search keeps the index of a project's content that project search
// reads, and answers the search from it (PRD FR-PROJ-03, plan note N57):
// PostgreSQL's full-text search over a document of each node, connection,
// variable, and component of a project (Document), compared without regard
// to case or diacritics, through the search configuration that migration
// 00029 creates, which reads every word of every language whole, with
// unaccent's dictionary (FR-PROJ-03 AC4).
//
// Each entity has its document whether it is in the trash or not, which the
// derivation that Register registers with the pipeline writes once every
// command of a batch has applied, for each entity that the batch may have
// changed the document of, in the batch's transaction, so that the index is
// always what the content holds, as a recomputation from scratch gives
// (searchtest.Drift). The job that RegisterJobs registers writes the
// documents of the entities that have none of the current Version: the
// content that is there already when migration 00029 applies, and every
// document again once a version of the server changes what a document holds.
// A search reads the documents of the entities that are not in the trash
// alone, so that the trash changes no document (FR-PROJ-06 AC2).
package search

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/project"
)

// Kind is the kind of an entity that has a document, which a search's
// result is of, and which its filter keeps.
type Kind string

// The kinds of entity that have a document. Item 74 of the plan adds a
// comment's.
const (
	Node       Kind = "node"
	Connection Kind = "connection"
	Variable   Kind = "variable"
	Component  Kind = "component"
)

// kinds are the kinds of entity that have a document, in the order in which
// a search orders results of one rank, which is their names' order, and with
// the kind of the IDs of each.
var kinds = []struct {
	kind Kind
	ids  ids.Kind
}{{Component, ids.Component}, {Connection, ids.Connection}, {Node, ids.Node}, {Variable, ids.Variable}}

// Kinds returns the kinds of entity that have a document, in the order of
// their names.
func Kinds() []Kind {
	out := make([]Kind, len(kinds))
	for i, k := range kinds {
		out[i] = k.kind
	}
	return out
}

// idKind returns the kind of the IDs of the entities of the kind k, or zero
// for a kind that has no document.
func idKind(k Kind) ids.Kind {
	for _, c := range kinds {
		if c.kind == k {
			return c.ids
		}
	}
	return 0
}

// IsKind reports whether k is a kind of entity that has a document.
func IsKind(k Kind) bool { return idKind(k) != 0 }

// Version is the version of what a document holds, which each document
// records it was written of. A change of what NewDocument makes of an
// entity, or of which entities have one, increments it, so that the job of
// the index writes every document again, as it writes the documents that
// are missing (Reindex); a migration that changes the search configuration
// sets the documents' version to 0 instead, and deletes the projects' rows of
// search_state, so that the job writes them again, which recomputes their
// vectors, while a search still reads them meanwhile.
const Version = 1

// MaxQueryLength is how many characters a search's query has at most.
const MaxQueryLength = 200

// CheckQuery says what is wrong with q as a search's query, or returns "":
// at most MaxQueryLength characters, without control characters.
func CheckQuery(q string) string {
	switch {
	case utf8.RuneCountInString(q) > MaxQueryLength:
		return fmt.Sprintf("is longer than %d characters", MaxQueryLength)
	case !utf8.ValidString(q) || strings.IndexFunc(q, input.IsControl) >= 0:
		return "contains a control character"
	}
	return ""
}

// Span is a part of a result's title or snippet: a word that the query
// matched, or the text between such words.
type Span struct {
	Text  string `json:"text" doc:"The text of the part"`
	Match bool   `json:"match" doc:"Whether the part is a word that the query matched, which a client highlights"`
}

// Result is an entity whose document a search's query matches, as the API
// shows it, with its board and its node, by which a client opens it, and
// its title and a snippet of its body with the query's matches marked
// (FR-PROJ-03 AC2).
type Result struct {
	Type Kind   `json:"type" enum:"node,connection,variable,component" doc:"The kind of the entity: a node, a connection, a variable, or a component"`
	ID   string `json:"id" doc:"The entity's ID"`
	// BoardID and BoardName are those of a node, a connection, and a
	// variable of a board, and none of a global variable or a component.
	BoardID   string  `json:"board_id,omitempty" doc:"The ID of the entity's board: a node's, a connection's, or a board variable's. A global variable and a component have none."`
	BoardName *string `json:"board_name,omitempty" doc:"The name of the entity's board, which a result has exactly when it has board_id"`
	// NodeID and NodeTitle are those of a node itself, and of the node that
	// a connection leaves.
	NodeID    string  `json:"node_id,omitempty" doc:"The ID of the node that a client opens the result by: a node's own, or the node that a connection leaves. A variable and a component have none."`
	NodeTitle *string `json:"node_title,omitempty" doc:"The title of the result's node, which may be empty, and which a result has exactly when it has node_id"`
	Title     []Span  `json:"title" nullable:"false" doc:"The entity's title, whole, in parts, with the words that the query matched marked: a node's title, a variable's key, or a component's name. A connection has none, and an empty title no part."`
	Snippet   []Span  `json:"snippet" nullable:"false" doc:"The entity's body, in parts, with the words that the query matched marked: whole where it has at most 35 words, or else 15 to 35 of its words from the start of its best match, or from its start where the query matched only the title. The body is a node's text, a connection's label, a variable's description, or a component's key, description, and values of text, each on a line of its own. An empty body has no part."`
	key       Key
}

// Key is where a result is in its list (Result.Key), by which a page of the
// list says where the next one starts: its rank, its kind, and its entity's
// UUID. The zero Key is before every result.
type Key struct {
	Rank float32
	Kind Kind
	ID   ids.UUID
}

// Key returns where r is in its list.
func (r Result) Key() Key { return r.key }

// Options configure a Service.
type Options struct {
	// Projects reads the projects, which authorises each search of their
	// content. It is required.
	Projects *project.Service
}

// Service answers the searches of the projects' content.
type Service struct {
	db       project.DB
	projects *project.Service
}

// New returns a Service that reads the index in db, with opts. It panics if
// opts.Projects is nil.
func New(db project.DB, opts Options) *Service {
	if opts.Projects == nil {
		panic("search: a Service needs the projects' service that authorises its searches")
	}
	return &Service{db: db, projects: opts.Projects}
}

// Search returns at most limit of the entities of the project projectID that
// are not in its trash whose documents query matches, of the kind, or of
// every kind for "", in the order of their keys (Key): of their ranks,
// highest first, then of their kinds' names and their entities' UUIDs; the
// first, if after is zero, or else those after it. query matches a document
// that holds a word that starts with each of its words, as the search
// configuration reads them, lowercased and without their diacritics, so that
// "ela tru" matches "Elara trusts", and a query without a word matches
// nothing. A match in a title ranks above one in a body (ts_rank, of the
// weights of migration 00029). It returns the errors of project.Service.Get
// first, as project.ErrNotFound, so that what a query holds is checked only
// for an actor who may read the project, and then input.FieldErrors at q for
// a query that CheckQuery refuses. The route's schema refuses a query longer
// than MaxQueryLength before, as it refuses any parameter that breaks it,
// whoever sends it, which tells nothing of the project.
func (s *Service) Search(ctx context.Context, actor commands.Actor, projectID ids.UUID, query string, kind Kind,
	after Key, limit int) ([]Result, error) {
	if kind != "" && !IsKind(kind) {
		return nil, fmt.Errorf("search: no kind %q", kind)
	}
	if after.Kind != "" && !IsKind(after.Kind) {
		return nil, fmt.Errorf("search: no kind %q", after.Kind)
	}
	out := []Result{}
	err := s.projects.ReadContent(ctx, s.db, actor, projectID, func(tx pgx.Tx, workspaceID ids.UUID) error {
		if problem := CheckQuery(query); problem != "" {
			return input.FieldErrors{{Field: "q", Message: problem}}
		}
		q := queries.New(tx)
		lexemes, err := q.SearchLexemes(ctx, query)
		if err != nil {
			return fmt.Errorf("search: read the query's words: %w", err)
		}
		if len(lexemes) == 0 {
			return nil
		}
		rows, err := q.Search(ctx, queries.SearchParams{Query: PrefixQuery(lexemes), WorkspaceID: nullUUID(workspaceID),
			ProjectID: nullUUID(projectID), Kind: string(kind), After: after.Kind != "", AfterRank: after.Rank,
			AfterKind: string(after.Kind), AfterID: pgtype.UUID{Bytes: after.ID, Valid: true},
			MaxRows: int32(min(limit, 1<<20))})
		if err != nil {
			return fmt.Errorf("search: read the results: %w", err)
		}
		for _, r := range rows {
			res, err := resultOf(r)
			if err != nil {
				return err
			}
			out = append(out, res)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// resultOf returns the row r of a search as the API shows it.
func resultOf(r queries.SearchRow) (Result, error) {
	k := idKind(Kind(r.Kind))
	if k == 0 {
		return Result{}, fmt.Errorf("search: a result of the kind %q, which has no document", r.Kind)
	}
	res := Result{Type: Kind(r.Kind), ID: ids.Encode(k, r.EntityID.Bytes), BoardID: encode(ids.Board, r.BoardID),
		NodeID: encode(ids.Node, r.NodeID), Title: Spans(r.Title), Snippet: Spans(r.Snippet),
		key: Key{Rank: r.Rank, Kind: Kind(r.Kind), ID: r.EntityID.Bytes}}
	if r.BoardID.Valid {
		res.BoardName = &r.BoardName.String
	}
	if r.NodeID.Valid {
		res.NodeTitle = &r.NodeTitle.String
	}
	return res, nil
}

// The characters that ts_headline marks the start and the end of each match
// with (queries.Search), which no document holds (NewDocument).
const (
	startMatch = '\x02'
	endMatch   = '\x03'
)

// Spans returns the parts of s, a title or a body as ts_headline marks the
// query's matches in it (queries.Search): the text between a start and an
// end of a match a part that is a match, and the rest parts that are not,
// each as long as it can be, and none for "".
func Spans(s string) []Span {
	out := []Span{}
	match := false
	for s != "" {
		i := strings.IndexAny(s, string([]rune{startMatch, endMatch}))
		if i < 0 {
			i = len(s)
		}
		if i > 0 {
			if n := len(out); n > 0 && out[n-1].Match == match {
				out[n-1].Text += s[:i]
			} else {
				out = append(out, Span{Text: s[:i], Match: match})
			}
		}
		if i == len(s) {
			break
		}
		match = s[i] == startMatch
		s = s[i+1:]
	}
	return out
}

// PrefixQuery returns the text of the tsquery that matches a document that
// holds a word that starts with each of lexemes, the words of a query as the
// search configuration reads them: each quoted, as a tsquery's text quotes a
// word, with :* after it, and joined by &.
func PrefixQuery(lexemes []string) string {
	terms := make([]string, len(lexemes))
	for i, l := range lexemes {
		terms[i] = "'" + strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(l) + "':*"
	}
	return strings.Join(terms, " & ")
}

// nullUUID returns id as a uuid column, null for zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}

// encode returns the public ID of the kind of id, or "" for null.
func encode(k ids.Kind, id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return ids.Encode(k, id.Bytes)
}
