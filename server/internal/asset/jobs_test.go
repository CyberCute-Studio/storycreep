package asset

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/database"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/dbtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/database/queries"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

func TestMain(m *testing.M) {
	os.Exit(dbtest.Run(m))
}

// errRefused is the error of a deletion that refusingStore refuses.
var errRefused = errors.New("the store refuses to delete the blob")

// refusingStore is a store that refuses to delete the blob of the key refuse,
// as a bucket whose policy denies it, and takes a while to delete the blob
// of the key slow.
type refusingStore struct {
	blob.Store
	refuse, slow string
}

func (s refusingStore) Delete(ctx context.Context, key string) error {
	switch key {
	case s.refuse:
		return errRefused
	case s.slow:
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.Delete(ctx, key)
}

// TestReconcileGoesOn checks that the reconciliation of a workspace goes on
// past a batch of blobs that no row refers to which fails, as when the store
// refuses to delete one of them, the first of the workspace's keys, so that
// the blobs of the next batch are deleted all the same, and that a batch
// holds its locks until its deletions end, however short a time the database
// lets a transaction wait idle, which would otherwise end the batch's session
// while it waited for the store, and fail it.
func TestReconcileGoesOn(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	if _, err := d.Admin(t).Exec(ctx, "ALTER DATABASE "+pgx.Identifier{d.Name}.Sanitize()+
		" SET idle_in_transaction_session_timeout = '100ms'"); err != nil {
		t.Fatal(err)
	}
	local, err := blob.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	workspaceID := ids.New()
	var keys []string
	for i := range orphanBatch + 4 {
		var sum [32]byte
		sum[31] = byte(i)
		key := blobKey(workspaceID, sum[:], "png")
		if err := local.Put(ctx, key, []byte("x"), MimePNG); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	store := refusingStore{Store: local, refuse: keys[0], slow: keys[orphanBatch]}
	s := &Service{db: d.Open(t)}
	deleted, err := s.reconcileWorkspace(ctx, store, workspaceID, func() bool { return true })
	joined, _ := err.(interface{ Unwrap() []error })
	if !errors.Is(err, errRefused) || joined == nil || len(joined.Unwrap()) != 1 || deleted != len(keys)-1 {
		t.Errorf("the reconciliation deleted %d blobs, and failed with %v; want %d deleted, and the refused "+
			"deletion's batch alone failed", deleted, err, len(keys)-1)
	}
	var left []string
	if err := local.List(ctx, workspaceID.String()+"/", "", func(i blob.Info) error {
		left = append(left, i.Key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(left, keys[:1]) {
		t.Errorf("the store holds %v, want the blob that it refused to delete alone", left)
	}
}

// TestKeepSession checks the timeouts that a batch of orphans sets for its
// transaction (queries.KeepSession): whatever the database sets, the
// transaction may wait idle for keepSessionIdle, and run for as long as it
// needs, and once it ends, its connection has the database's timeouts again.
func TestKeepSession(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	conn := d.Admin(t)
	if _, err := conn.Exec(ctx, "SET idle_in_transaction_session_timeout = '1s'"); err != nil {
		t.Fatal(err)
	}
	// The settings that the server has, as PostgreSQL 17 added
	// transaction_timeout, with what each is in milliseconds.
	settings := func() map[string]string {
		t.Helper()
		rows, err := conn.Query(ctx, "SELECT name, setting FROM pg_catalog.pg_settings WHERE name IN "+
			"('idle_in_transaction_session_timeout', 'transaction_timeout')")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for rows.Next() {
			var name, setting string
			if err := rows.Scan(&name, &setting); err != nil {
				t.Fatal(err)
			}
			got[name] = setting
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return got
	}
	before := settings()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := queries.New(tx).KeepSession(ctx, strconv.FormatInt(keepSessionIdle.Milliseconds(), 10)); err != nil {
		t.Fatal(err)
	}
	in := settings()
	if want := strconv.FormatInt(keepSessionIdle.Milliseconds(), 10); in["idle_in_transaction_session_timeout"] !=
		want {
		t.Errorf("in the transaction, idle_in_transaction_session_timeout is %s, want %s",
			in["idle_in_transaction_session_timeout"], want)
	}
	if v, ok := in["transaction_timeout"]; ok && v != "0" {
		t.Errorf("in the transaction, transaction_timeout is %s, want 0", v)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if after := settings(); !maps.Equal(after, before) || after["idle_in_transaction_session_timeout"] != "1000" {
		t.Errorf("after the transaction, the connection's timeouts are %v, want %v", after, before)
	}
}

// TestListWorkspace checks that a workspace's listing, which starts after a
// key picked at random, lists each of its blobs once, and no other
// workspace's, whatever key it starts after, which it holds in their order,
// and covers every key once it is done.
func TestListWorkspace(t *testing.T) {
	store, err := blob.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
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
		if !l.done || !slices.Equal(l.keys, want) {
			t.Fatalf("the listing after %s, done %t, lists %d keys, want each of %d once, in order", l.from, l.done,
				len(l.keys), len(want))
		}
		if !l.has(want[0]) || !l.has(want[len(want)-1]) || l.has(blobKey(theirs, []byte{0}, "png")) {
			t.Errorf("a listing that is done does not have its own keys, or has another workspace's")
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

// TestStorageClose checks that closing the storage closes the store that it
// opened, which holds the local driver's directory open, and that the
// storage opens a store anew when it is asked for one after.
func TestStorageClose(t *testing.T) {
	ctx := context.Background()
	storage := NewStorage(noWorkspace{}, StorageOptions{Configured: &config.AssetStorage{Dir: t.TempDir()}})
	t.Cleanup(func() { _ = storage.Close() })
	key := blobKey(ids.New(), []byte{1}, "png")
	store, err := storage.Store(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, key, []byte("x"), MimePNG); err == nil {
		t.Error("a store that the storage closed takes a blob")
	}
	again, err := storage.Store(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Put(ctx, key, []byte("x"), MimePNG); err != nil || again == store {
		t.Errorf("the store opened after the storage closed: %v, the closed one %t", err, again == store)
	}
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
