package sweep_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/sweep"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// TestWorkspaces checks that a run reaches every workspace once, going on
// from one picked at random in the order of their IDs and then on from the
// first, so that runs start at different workspaces, whether a page of their
// IDs holds one, a few, or all of them, so that a run reads every page of
// both stretches; that it goes on past a workspace whose work fails, and
// returns the error; and that once the run has no time left, it starts no
// other workspace, and ends without an error, so that a job never runs past
// its minute however many workspaces there are.
func TestWorkspaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := dbtest.New(t)
	db, admin := d.Open(t), d.Admin(t)
	var all []ids.UUID
	for range 5 {
		ws := ids.New()
		if _, err := admin.Exec(ctx, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', $2, "+
			"'unlimited')", ws, "harbour-"+ws.String()); err != nil {
			t.Fatal(err)
		}
		all = append(all, ws)
	}
	slices.SortFunc(all, func(a, b ids.UUID) int { return bytes.Compare(a[:], b[:]) })
	always := func() bool { return true }
	starts := map[ids.UUID]bool{}
	failed := errors.New("the work failed")
	for i := range 45 {
		// A page of one or two IDs, which the workspaces fill several of, and
		// one of 100, which holds them all.
		size := []int32{1, 2, 100}[i%3]
		var got []ids.UUID
		err := sweep.WorkspacesPaged(ctx, db, size, always, func(ws ids.UUID) error {
			got = append(got, ws)
			if ws == all[2] {
				return failed
			}
			return nil
		})
		if !errors.Is(err, failed) {
			t.Fatalf("a run whose work failed for a workspace, with pages of %d: %v, want its error", size, err)
		}
		if len(got) == 0 {
			t.Fatalf("a run with pages of %d reached no workspace", size)
		}
		first := slices.Index(all, got[0])
		if first < 0 {
			t.Fatalf("a run started at %v, which is not a workspace", got[0])
		}
		if want := append(slices.Clone(all[first:]), all[:first]...); !slices.Equal(got, want) {
			t.Fatalf("a run with pages of %d reached %v, want %v", size, got, want)
		}
		starts[got[0]] = true
	}
	if len(starts) < 2 {
		t.Errorf("45 runs all started at the workspace %v", starts)
	}

	left := 2
	var got []ids.UUID
	err := sweep.WorkspacesPaged(ctx, db, 1, func() bool { left--; return left >= 0 }, func(ws ids.UUID) error {
		got = append(got, ws)
		return nil
	})
	if err != nil || len(got) != 2 {
		t.Errorf("a run with time for two workspaces reached %d, %v; want 2 without an error", len(got), err)
	}
}
