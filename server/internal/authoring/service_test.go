package authoring

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// refusingDB is a database that takes no transaction.
type refusingDB struct{ DB }

func (refusingDB) InTx(context.Context, database.Scope, func(pgx.Tx) error) error {
	return errors.New("the database is down")
}

// TestEndsTheExport checks which failures of a build end its export at once:
// those that another attempt would repeat, among them the attempt's own
// timeout, and not the server's stopping, after which the build runs again.
func TestEndsTheExport(t *testing.T) {
	t.Parallel()
	timedOut, cancelTimeout := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancelTimeout()
	stopped, stop := context.WithCancel(t.Context())
	stop()
	transient := errors.New("the store did not answer")
	for _, c := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"success", t.Context(), nil, false},
		{"success as the time runs out", timedOut, nil, false},
		{"a transient failure", t.Context(), transient, false},
		{"the server stopping", stopped, transient, false},
		{"the attempt's timeout", timedOut, transient, true},
		{"a project in the trash", t.Context(), errProjectGone, true},
		{"an asset's file gone", t.Context(), fmt.Errorf("%w: assets/a.png", errAssetMissing), true},
	} {
		if got := endsTheExport(c.ctx, c.err); got != c.want {
			t.Errorf("%s: endsTheExport is %v, want %v", c.name, got, c.want)
		}
	}
}

// TestFailWaitsForTheDatabase checks that a build whose export could not be
// failed runs again rather than end, which would leave the export on its way
// and keep its user from asking for another until it expires.
func TestFailWaitsForTheDatabase(t *testing.T) {
	t.Parallel()
	s := New(refusingDB{}, Options{})
	err := s.fail(t.Context(), ids.New(), ids.New(), errors.New("every attempt failed"))
	if !errors.As(err, new(*river.JobSnoozeError)) {
		t.Errorf("fail returned %v, want a snooze of the job", err)
	}
}
