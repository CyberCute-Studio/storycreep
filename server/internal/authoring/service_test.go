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

// endedDB is a database in which the export has ended, so that failing it
// changes no row.
type endedDB struct{ DB }

func (endedDB) InTx(_ context.Context, _ database.Scope, fn func(pgx.Tx) error) error {
	return fn(noRowsTx{})
}

// noRowsTx is a transaction whose every query finds no row.
type noRowsTx struct{ pgx.Tx }

func (noRowsTx) QueryRow(context.Context, string, ...any) pgx.Row { return noRow{} }

type noRow struct{}

func (noRow) Scan(...any) error { return pgx.ErrNoRows }

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

// TestFailLeavesAnEndedExport checks that the job of an export that has ended,
// as a ready one whose job ran again, completes rather than report a failure.
func TestFailLeavesAnEndedExport(t *testing.T) {
	t.Parallel()
	s := New(endedDB{}, Options{})
	if err := s.fail(t.Context(), ids.New(), ids.New(), errors.New("every attempt failed")); err != nil {
		t.Errorf("fail returned %v, want nil", err)
	}
}

// TestOnlyFails checks that the attempts after the builds only fail the export
// and that more than one does, as River discards a job whose last attempt the
// process's death cut off, which would leave the export on its way, and that a
// run after a snooze, which keeps its attempt's number, does not build again.
func TestOnlyFails(t *testing.T) {
	t.Parallel()
	last := buildAttempts + failAttempts
	for attempt, want := range map[int]bool{1: false, buildAttempts: false, buildAttempts + 1: true,
		last - 1: true, last: true} {
		if got := onlyFails(attempt, 0); got != want {
			t.Errorf("onlyFails(%d, 0) is %v, want %v", attempt, got, want)
		}
		if !onlyFails(attempt, 1) {
			t.Errorf("onlyFails(%d, 1) is false, so a snoozed job would build again", attempt)
		}
	}
	if last-1 <= buildAttempts {
		t.Errorf("only the job's last attempt, %d, can fail the export", last)
	}
}
