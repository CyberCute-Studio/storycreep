package apitoken

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/audit"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/isolationtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/input"
	"github.com/CyberCute-Studio/storycreep/server/internal/scope"
)

// TestInsertJudgesExpiryByTheDatabase checks that a token is not made with
// an expiry that is not after the database's time as it is made, which
// judges every use of the token, whatever the server's clock said of it
// (check), as when that clock is behind the database's; and that the
// refusal leaves neither the token nor its audit entry.
//
// Verifies: NFR-SEC-05
func TestInsertJudgesExpiryByTheDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := isolationtest.New(t, dbtest.New(t), isolationtest.Tables)
	w := f.A
	issue := func(expires func(now time.Time) time.Time) error {
		return f.DB.InTx(ctx, w.Scope(), func(tx pgx.Tx) error {
			var now time.Time
			if err := tx.QueryRow(ctx, "SELECT now()").Scan(&now); err != nil {
				return err
			}
			at := expires(now)
			_, err := insert(ctx, tx, w.UserID, w.ID, ids.UUID{}, Spec{Name: "late", Scopes: []scope.Scope{
				scope.SnapshotRead}, ExpiresAt: &at}, w.UserID, audit.Client{})
			return err
		})
	}
	for name, expires := range map[string]func(time.Time) time.Time{
		"the database's time":                           func(now time.Time) time.Time { return now },
		"less than a microsecond after it":              func(now time.Time) time.Time { return now.Add(time.Nanosecond) },
		"a second before the database's time":           func(now time.Time) time.Time { return now.Add(-time.Second) },
		"the zero time, which is a time like any other": func(time.Time) time.Time { return time.Time{} },
	} {
		var fields input.FieldErrors
		if err := issue(expires); !errors.As(err, &fields) || !slices.Equal(fields, input.FieldErrors{expiredField}) {
			t.Errorf("an expiry at %s: %v, want the expiry's field error", name, err)
		}
	}
	var tokens, entries int
	if err := f.Admin().QueryRow(ctx, `SELECT (SELECT count(*) FROM api_token WHERE name = 'late'),
		(SELECT count(*) FROM audit_entry WHERE action = 'token.create')`).Scan(&tokens, &entries); err != nil {
		t.Fatal(err)
	}
	if tokens != 0 || entries != 0 {
		t.Errorf("the refusals left %d tokens and %d entries of their making", tokens, entries)
	}
	if err := issue(func(now time.Time) time.Time { return now.Add(time.Microsecond) }); err != nil {
		t.Errorf("an expiry a microsecond after the database's time: %v", err)
	}
}
