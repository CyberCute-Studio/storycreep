// Package audit records the audit log (PRD FR-TEN-07, §9.6): an entry for
// each security- or tenancy-relevant event, appended to audit_entry in the
// transaction of the change that it records, so that the entry commits
// exactly when the change does (AC3). The application may add and read
// entries, but neither change nor delete them (AC1), which the migrations'
// grants enforce: only the audit retention job deletes them, through the
// maintenance path (§5.4).
//
// Each type of event is registered in events, with its action, whether it
// belongs to a workspace, the kind of entity that it is about, the results
// that it can have, and the keys that its metadata may hold, and Record
// refuses an entry that its registration does not allow.
//
// An entry names its actors and its subject by ID only, never by email
// address or display name (§9.6). A failed login for an address that belongs
// to no account, which has no ID, records the address as its keyed hash
// instead: HMAC-SHA256 with a key that migrating generated for the database,
// so that the hashes of one address match each other, but nobody who has the
// log without the key can tell which address they are of (Entry.AttemptedEmail).
package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/logging"
)

// Client is what the audit log records of the client that makes a request.
type Client struct {
	// IP is the client's address, or zero if it is unknown.
	IP netip.Addr
	// UserAgent is the client's User-Agent header.
	UserAgent string
}

// Entry is an event to record.
type Entry struct {
	Action Action
	// WorkspaceID is the workspace that the event belongs to, which must be
	// the workspace of the transaction that records it, or zero for an event
	// that belongs to no workspace, which a transaction without a workspace
	// records.
	WorkspaceID ids.UUID
	// ActorUserID is the user who acted, and ActorTokenID the API token that
	// they acted with, each zero if there is none, as for a failed login,
	// which nobody is signed in for.
	ActorUserID, ActorTokenID ids.UUID
	// SubjectID is the entity that the event is about, of the kind that its
	// registration names, or zero if there is none.
	SubjectID ids.UUID
	Result    Result
	// IP is the client's address, without its zone, and UserAgent its
	// User-Agent header, each zero if it is unknown. The client chooses its
	// user agent, so Record keeps at most MaxUserAgentBytes of it, and
	// replaces what a text column cannot hold.
	IP        netip.Addr
	UserAgent string
	// Metadata are the details of the event, under the keys that its
	// registration allows. Each value must encode as JSON. Like the rest of
	// the entry, they name people by ID only, and hold no text that a user
	// wrote, which could be anything.
	Metadata map[string]any
	// AttemptedEmail is the email address that a failed login was for, when
	// the address belongs to no account, so that SubjectID is zero. Record
	// stores only its keyed hash, as the metadata's emailHashKey, of the
	// address in lower case, as the database compares addresses.
	AttemptedEmail string
}

// emailHashKey is the metadata key under which an entry holds the keyed hash
// of its AttemptedEmail, in lowercase hexadecimal.
const emailHashKey = "email_hmac"

// MaxUserAgentBytes bounds the user agent that an entry keeps, so that a
// client cannot fill the log with a long one. A longer one is cut at a
// character's boundary. Other packages that keep a client's user agent, such
// as the sessions, keep as much of it (ClientText).
const MaxUserAgentBytes = 512

// Record appends e to the audit log in tx, the transaction of the change that
// e records, so that the entry commits if and only if the change does
// (FR-TEN-07 AC3). Its request ID is the one that ctx carries
// (logging.RequestID), if any. tx is a transaction of the application login,
// whose scope (database.Scope) has e's workspace, or no workspace for an
// event that belongs to none, or of the maintenance path. Record returns an
// error, and adds nothing, if e is not what its event's registration allows,
// or if the database refuses it, as row-level security does an entry of
// another workspace than the transaction's. The caller then rolls the change
// back with the transaction, so that no change goes unrecorded.
//
// An event that is a refusal, such as a failed login, changes nothing else:
// the transaction that records it must commit, although the request fails.
func Record(ctx context.Context, tx pgx.Tx, e Entry) error {
	s, err := check(e)
	if err != nil {
		return err
	}
	q := queries.New(tx)
	metadata := maps.Clone(e.Metadata)
	// A failure of an event that records attempted addresses hashes one
	// whether or not the entry keeps the hash, so that a failed login that
	// names the account that the address belongs to does the same work as
	// one for an address that belongs to none, and its time does not tell
	// whether the address has an account (PRD FR-AUTH-01).
	if e.AttemptedEmail != "" || s.attemptedEmail && e.Result == ResultFailure {
		in, err := q.AuditEmailHashInput(ctx, ClientText(e.AttemptedEmail, 0))
		if err != nil {
			return fmt.Errorf("audit: read the key of the email hash: %w", err)
		}
		mac := hmac.New(sha256.New, in.Key)
		mac.Write([]byte(in.Email))
		sum := hex.EncodeToString(mac.Sum(nil))
		if e.AttemptedEmail != "" {
			if metadata == nil {
				metadata = map[string]any{}
			}
			metadata[emailHashKey] = sum
		}
	}
	encoded := []byte("{}")
	if len(metadata) > 0 {
		if encoded, err = json.Marshal(metadata); err != nil {
			return fmt.Errorf("audit: encode the metadata of %s: %w", e.Action, err)
		}
	}
	var subjectKind pgtype.Text
	if e.SubjectID != (ids.UUID{}) {
		subjectKind = pgtype.Text{String: s.subject, Valid: true}
	}
	var ip *netip.Addr
	if e.IP.IsValid() {
		// An inet holds no zone, such as the %eth0 of a link-local IPv6
		// address.
		addr := e.IP.WithZone("")
		ip = &addr
	}
	err = q.InsertAuditEntry(ctx, queries.InsertAuditEntryParams{
		ID:           nullUUID(ids.New()),
		WorkspaceID:  nullUUID(e.WorkspaceID),
		ActorUserID:  nullUUID(e.ActorUserID),
		ActorTokenID: nullUUID(e.ActorTokenID),
		Action:       string(e.Action),
		SubjectKind:  subjectKind,
		SubjectID:    nullUUID(e.SubjectID),
		Result:       string(e.Result),
		Ip:           ip,
		UserAgent:    nullText(ClientText(e.UserAgent, MaxUserAgentBytes)),
		RequestID:    nullText(ClientText(logging.RequestID(ctx), 0)),
		Metadata:     encoded,
	})
	if err != nil {
		return fmt.Errorf("audit: record %s: %w", e.Action, err)
	}
	return nil
}

// check returns the registration of e's action, or an error if e is not what
// the registration allows.
func check(e Entry) (eventSpec, error) {
	s, ok := lookup(e.Action)
	if !ok {
		return eventSpec{}, fmt.Errorf("audit: the action %q is not registered", e.Action)
	}
	var none ids.UUID
	switch {
	case s.inWorkspace && e.WorkspaceID == none:
		return s, fmt.Errorf("audit: %s belongs to a workspace, but the entry has none", e.Action)
	case !s.inWorkspace && e.WorkspaceID != none:
		return s, fmt.Errorf("audit: %s belongs to no workspace, but the entry has one", e.Action)
	case !slices.Contains(s.results, e.Result):
		return s, fmt.Errorf("audit: %s cannot have the result %q", e.Action, e.Result)
	case s.subject == "" && e.SubjectID != none:
		return s, fmt.Errorf("audit: %s has no subject, but the entry has one", e.Action)
	}
	for _, k := range slices.Sorted(maps.Keys(e.Metadata)) {
		if !slices.Contains(s.metadata, k) {
			return s, fmt.Errorf("audit: %s has no metadata %q", e.Action, k)
		}
	}
	if e.AttemptedEmail != "" {
		switch {
		case !s.attemptedEmail:
			return s, fmt.Errorf("audit: %s records no attempted email address", e.Action)
		case e.Result != ResultFailure:
			return s, fmt.Errorf("audit: %s records an attempted email address only when it fails", e.Action)
		case e.SubjectID != none:
			return s, fmt.Errorf("audit: %s names the account that the address belongs to, so it records no "+
				"attempted address", e.Action)
		}
	}
	return s, nil
}

// ClientText returns s, text that a client chose, as a text column can hold
// it, so that a client's text cannot make the entry, and the change that it
// records, fail: as valid UTF-8, with U+FFFD in place of each run of bytes
// that is not, and without NUL characters, both of which PostgreSQL refuses.
// If limit is not zero, it is cut to at most limit bytes, at a character's
// boundary. Other packages that store a client's text, such as the user
// agent of a session, keep it the same way.
func ClientText(s string, limit int) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, string(utf8.RuneError)), "\x00", "")
	if limit == 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// nullUUID returns id as a column of the uuid type, null if id is zero.
func nullUUID(id ids.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != (ids.UUID{})}
}

// nullText returns s as a text column, null if s is empty.
func nullText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
