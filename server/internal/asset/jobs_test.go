package asset

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

// TestListWorkspace checks that a workspace's listing, which starts after a
// key picked at random, lists each of its blobs once, and no other
// workspace's, whatever key it starts after, and covers every key once it is
// done.
func TestListWorkspace(t *testing.T) {
	store, err := blob.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mine, theirs := ids.New(), ids.New()
	var want []string
	for i := range 40 {
		sum := []byte{byte(i * 6), byte(i)}
		key := blobKey(mine, sum, "png")
		want = append(want, key)
		for _, k := range []string{key, blobKey(theirs, sum, "png")} {
			if err := store.Put(ctx, k, []byte("x"), MimePNG); err != nil {
				t.Fatal(err)
			}
		}
	}
	slices.Sort(want)
	for range 20 {
		l, err := listWorkspace(ctx, store, mine, func() bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		got := slices.Sorted(slices.Values(l.ordered))
		if !l.done || !slices.Equal(got, want) {
			t.Fatalf("the listing after %s, done %t, lists %d keys, want each of %d once", l.from, l.done, len(got),
				len(want))
		}
		if !l.covers(want[0]) || !l.covers(want[len(want)-1]) {
			t.Errorf("a listing that is done does not cover every key")
		}
	}
}

// TestListingCovers checks which keys a listing that stopped early covers:
// those after the key that it started after, up to the last that it listed,
// and, once it went on from the first, all of those, and the first ones up
// to the last that it listed since.
func TestListingCovers(t *testing.T) {
	early := &listing{from: "w/5", last: "w/7.png"}
	wrapped := &listing{from: "w/5", last: "w/2.png", wrapped: true}
	for _, tt := range []struct {
		l    *listing
		key  string
		want bool
	}{
		{early, "w/6.png", true},
		{early, "w/7.png", true},
		{early, "w/8.png", false},
		{early, "w/1.png", false},
		{wrapped, "w/8.png", true},
		{wrapped, "w/1.png", true},
		{wrapped, "w/2.png", true},
		{wrapped, "w/3.png", false},
		{&listing{from: "w/5", done: true}, "w/3.png", true},
	} {
		if got := tt.l.covers(tt.key); got != tt.want {
			t.Errorf("a listing from %s to %s, wrapped %t, covers %s: %t, want %t", tt.l.from, tt.l.last,
				tt.l.wrapped, tt.key, got, tt.want)
		}
	}
}

// noWorkspace is a DB of an instance that has no workspace, as one whose
// setup is pending, and no row of its own: it runs no transaction.
type noWorkspace struct{}

func (noWorkspace) InTx(context.Context, database.Scope, func(pgx.Tx) error) error {
	return nil
}

// TestReconcileWithoutWorkspaces checks that the reconciliation of an
// instance that has no workspace yet opens no store, as the local driver's
// directory, which it might not be able to create, but does nothing.
func TestReconcileWithoutWorkspaces(t *testing.T) {
	// A directory under a file cannot be created.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	storage := NewStorage(noWorkspace{}, StorageOptions{Dir: filepath.Join(file, "assets")})
	if _, err := storage.Store(context.Background()); err == nil {
		t.Fatal("a directory under a file opens")
	}
	s := &Service{db: noWorkspace{}, storage: storage}
	if n, err := s.Reconcile(context.Background()); n != 0 || err != nil {
		t.Errorf("the reconciliation of an instance without workspaces: %d, %v; want 0, nil", n, err)
	}
}
