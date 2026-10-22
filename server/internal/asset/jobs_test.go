package asset

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

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
	dir := t.TempDir()
	local, err := blob.OpenLocal(dir)
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
		age(t, dir, key)
		keys = append(keys, key)
	}
	slices.Sort(keys)
	store := refusingStore{Store: local, refuse: keys[0], slow: keys[orphanBatch]}
	s := &Service{db: d.Open(t)}
	cl := claimed(t, local, "a")
	deleted, err := s.reconcileWorkspace(ctx, store, workspaceID, cl, func() bool { return true })
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

// TestDeleteBlobsOutlastContext checks that a deletion of a batch of orphans
// that was sent before the job's context ended is not given up on when it
// ends, as the store might carry it out after the batch's locks are
// released, but waited for, and that none is sent once the context has less
// than orphanDeleteTimeout left, which is left to the next run.
func TestDeleteBlobsOutlastContext(t *testing.T) {
	dir := t.TempDir()
	local, err := blob.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	workspaceID := ids.New()
	put := func(b byte) string {
		key := blobKey(workspaceID, []byte{b}, "png")
		if err := local.Put(context.Background(), key, []byte("x"), MimePNG); err != nil {
			t.Fatal(err)
		}
		age(t, dir, key)
		return key
	}
	stored := func(key string) bool {
		_, err := local.Stat(context.Background(), key)
		return err == nil
	}
	slow := put(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(100*time.Millisecond, cancel)
	if n, err := deleteBlobs(ctx, refusingStore{Store: local, slow: slow}, []string{slow}); n != 1 || err != nil ||
		stored(slow) {
		t.Errorf("a deletion whose context ended while it went on deleted %d blobs: %v, stored %t; want 1", n, err,
			stored(slow))
	}
	late := put(2)
	short, cancelShort := context.WithTimeout(context.Background(), orphanDeleteTimeout/2)
	defer cancelShort()
	if n, err := deleteBlobs(short, local, []string{late}); n != 0 || err != nil || !stored(late) {
		t.Errorf("a batch with less than orphanDeleteTimeout left deleted %d blobs: %v; want none, and no error", n,
			err)
	}
}

// TestDeleteOrphansAsksStore checks that a batch of orphans deletes a blob
// only once the store says, just before the deletion, that it was stored
// orphanAge ago, so that it keeps a blob that its listing found old, but that
// an upload stored again since, as one of an instance on a copy of the
// database, whose locks the batch's do not hold back, while it deletes the
// old one beside it, and counts none that the store no longer has.
func TestDeleteOrphansAsksStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	local, err := blob.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	workspaceID := ids.New()
	var batch []orphan
	for b := range byte(3) {
		sum := make([]byte, 32)
		sum[31] = b
		batch = append(batch, orphan{key: blobKey(workspaceID, sum, "png"), sum: sum})
	}
	old, again, gone := batch[0].key, batch[1].key, batch[2].key
	for _, key := range []string{old, again} {
		if err := local.Put(ctx, key, []byte("x"), MimePNG); err != nil {
			t.Fatal(err)
		}
		age(t, dir, key)
	}
	// The listing found both old; an upload then stored the second again.
	if err := local.Put(ctx, again, []byte("x"), MimePNG); err != nil {
		t.Fatal(err)
	}
	stored := func(key string) bool {
		_, err := local.Stat(ctx, key)
		return err == nil
	}
	s := &Service{db: dbtest.New(t).Open(t)}
	if n, err := s.deleteOrphans(ctx, local, workspaceID, batch); n != 1 || err != nil || stored(old) ||
		!stored(again) || stored(gone) {
		t.Errorf("the batch deleted %d blobs: %v, kept the old one %t and the one stored again %t; want the old "+
			"one deleted alone", n, err, stored(old), stored(again))
	}
}

// age makes the blob of key, in the local driver's directory dir, older than
// orphanAge, as one stored a day ago, which the reconciliation deletes if no
// row refers to it.
func age(t *testing.T, dir, key string) {
	t.Helper()
	long := time.Now().Add(-orphanAge - time.Hour)
	if err := os.Chtimes(filepath.Join(dir, filepath.FromSlash(key)), long, long); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileKeepsYoungBlobs checks that the reconciliation deletes a blob
// that no row refers to only once it was stored orphanAge ago, as the store
// says, so that an instance on a copy of another's database just made, which
// takes the store for its own until the other claims it again, deletes none
// of the files that the other stored since the copy was made.
func TestReconcileKeepsYoungBlobs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	local, err := blob.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	workspaceID := ids.New()
	put := func(b byte) string {
		var sum [32]byte
		sum[31] = b
		key := blobKey(workspaceID, sum[:], "png")
		if err := local.Put(ctx, key, []byte("x"), MimePNG); err != nil {
			t.Fatal(err)
		}
		return key
	}
	stored := func(key string) bool {
		_, err := local.Stat(ctx, key)
		return err == nil
	}
	old, young := put(1), put(2)
	age(t, dir, old)
	s := &Service{db: dbtest.New(t).Open(t)}
	always := func() bool { return true }
	if n, err := s.reconcileWorkspace(ctx, local, workspaceID, claimed(t, local, "a"), always); n != 1 || err != nil ||
		stored(old) || !stored(young) {
		t.Errorf("the run deleted %d blobs: %v, kept the old %t and the young %t; want the old one deleted alone",
			n, err, stored(old), stored(young))
	}
	age(t, dir, young)
	if n, err := s.reconcileWorkspace(ctx, local, workspaceID, claimed(t, local, "a"), always); n != 1 || err != nil ||
		stored(young) {
		t.Errorf("once the young blob is old, the run deleted %d blobs: %v; want it deleted", n, err)
	}
}

// TestEnsureBlobRefreshes checks that an upload that finds its blob stored
// already stores it again if no row of the workspace refers to it, and the
// store says that it is older than refreshAge, as one of a project purged
// an hour before, so that an instance on a copy of the database made before
// the upload, whose rows do not refer to it either, does not find it old
// enough to delete, but not if a row refers to it, or it is younger.
func TestEnsureBlobRefreshes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	local, err := blob.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	key := blobKey(ids.New(), make([]byte, 32), "png")
	img := Image{Mime: MimePNG, Ext: "png", Data: []byte("x")}
	if err := ensureBlob(ctx, local, key, img, true); err != nil {
		t.Fatal(err)
	}
	modified := func() time.Time {
		t.Helper()
		info, err := local.Stat(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		return info.Modified
	}
	for _, tt := range []struct {
		name              string
		old, unreferenced bool
		wantStoredAgain   bool
	}{
		{"a young blob that no row refers to", false, true, false},
		{"an old blob that a row refers to", true, false, false},
		{"an old blob that no row refers to", true, true, true},
	} {
		if tt.old {
			age(t, dir, key)
		}
		before := modified()
		if err := ensureBlob(ctx, local, key, img, tt.unreferenced); err != nil {
			t.Fatal(err)
		}
		if again := modified().After(before); again != tt.wantStoredAgain {
			t.Errorf("%s: stored again %t, want %t", tt.name, again, tt.wantStoredAgain)
		}
	}
}

// slowOwner is a store whose owner blob is read only once release is
// closed, after entered is.
type slowOwner struct {
	blob.Store
	entered, release chan struct{}
}

func (s slowOwner) Open(ctx context.Context, key string) (io.ReadCloser, blob.Info, error) {
	if key == OwnerKey {
		close(s.entered)
		<-s.release
	}
	return s.Store.Open(ctx, key)
}

// TestClaimLeavesInstanceRow checks that the claim of the store, which reads
// the store's owner blob under a lock that two runs of it take turns for,
// does not lock the instance's row while the store answers, which first-run
// setup locks too, so that setup does not wait for a store that is slow.
func TestClaimLeavesInstanceRow(t *testing.T) {
	ctx := context.Background()
	local, err := blob.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	d := dbtest.New(t)
	s := &Service{db: d.Open(t)}
	store := slowOwner{Store: local, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := s.claimStore(ctx, store)
		done <- err
	}()
	<-store.entered
	admin := d.Admin(t)
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, "SELECT FROM instance FOR UPDATE NOWAIT")
	_ = tx.Rollback(ctx)
	close(store.release)
	if err != nil {
		t.Errorf("the instance's row while the claim reads the store's owner blob: %v; want it unlocked", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// claimed writes the store's owner blob as a run of the database whose
// identity is self writes it, and returns the run's claim.
func claimed(t *testing.T, store blob.Store, self string) *claim {
	t.Helper()
	cl := &claim{self: self, content: self + " token\n"}
	if err := store.Put(context.Background(), OwnerKey, []byte(cl.content), "text/plain"); err != nil {
		t.Fatal(err)
	}
	return cl
}

// TestCheckClaim checks that a run that finds the store's owner blob changed
// before a batch of deletions, as by an instance that claimed the store at
// the same moment, on a copy of its database's files or on another
// database, or deleted, deletes no more blobs, and says why, and that one
// that finds its own claim deletes them.
func TestCheckClaim(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	local, err := blob.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	workspaceID := ids.New()
	key := blobKey(workspaceID, make([]byte, 32), "png")
	s := &Service{db: dbtest.New(t).Open(t)}
	always := func() bool { return true }
	for _, tt := range []struct {
		name, blob, owner string
		copy              bool
	}{
		{"a copy of the database", "a other\n", "a", true},
		{"another database", "b other\n", "b", false},
		{"no blob", "", "", false},
	} {
		if err := local.Put(ctx, key, []byte("x"), MimePNG); err != nil {
			t.Fatal(err)
		}
		age(t, dir, key)
		cl := claimed(t, local, "a")
		if tt.blob == "" {
			err = local.Delete(ctx, OwnerKey)
		} else {
			err = local.Put(ctx, OwnerKey, []byte(tt.blob), "text/plain")
		}
		if err != nil {
			t.Fatal(err)
		}
		n, err := s.reconcileWorkspace(ctx, local, workspaceID, cl, always)
		_, statErr := local.Stat(ctx, key)
		if n != 0 || err != nil || statErr != nil || cl.refused == nil || cl.refused.Owner != tt.owner ||
			cl.refused.Copy != tt.copy {
			t.Errorf("%s: the run deleted %d blobs: %v, refused %+v; want none, refused by %q, a copy %t", tt.name,
				n, err, cl.refused, tt.owner, tt.copy)
		}
	}
	cl := claimed(t, local, "a")
	if n, err := s.reconcileWorkspace(ctx, local, workspaceID, cl, always); n != 1 || err != nil ||
		cl.refused != nil {
		t.Errorf("the run that finds its own claim deleted %d blobs: %v, refused %+v; want 1", n, err, cl.refused)
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

// TestListWorkspace checks that a workspace's listing lists each of its blobs
// once, and no other workspace's, which it holds in their order, and covers
// every key once it is done: in one request from the first, for a workspace
// of at most smallListing blobs, and otherwise from after a key picked at
// random, whatever that key is.
func TestListWorkspace(t *testing.T) {
	dir := t.TempDir()
	local, err := blob.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	store := &countingStore{Store: local}
	ctx := context.Background()
	for _, n := range []int{40, smallListing + 40} {
		mine, theirs := ids.New(), ids.New()
		var want []string
		for i := range n {
			sum := []byte{byte(i * 6), byte(i), byte(i >> 8)}
			key := blobKey(mine, sum, "png")
			want = append(want, key)
			// The files are written as the local driver keeps them, without
			// its syncs, which a thousand of would take seconds.
			for _, k := range []string{key, blobKey(theirs, sum, "png")} {
				if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(k)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, k), []byte("x"), 0o640); err != nil {
					t.Fatal(err)
				}
			}
		}
		slices.Sort(want)
		for range 20 {
			store.lists = 0
			l, err := listWorkspace(ctx, store, mine, func() bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			if !l.done || !slices.Equal(l.keys, want) {
				t.Fatalf("the listing of %d blobs after %s, done %t, lists %d keys, want each once, in order", n,
					l.from, l.done, len(l.keys))
			}
			if n <= smallListing && store.lists != 1 {
				t.Errorf("the listing of %d blobs took %d requests, want 1", n, store.lists)
			}
			if !l.has(want[0]) || !l.has(want[len(want)-1]) || l.has(blobKey(theirs, []byte{0, 0, 0}, "png")) {
				t.Errorf("a listing that is done does not have its own keys, or has another workspace's")
			}
			if !l.covers(want[0]) || !l.covers(want[len(want)-1]) {
				t.Errorf("a listing that is done does not cover every key")
			}
		}
	}
}

// countingStore is a store that counts its listings.
type countingStore struct {
	blob.Store
	lists int
}

func (s *countingStore) List(ctx context.Context, prefix, after string, fn func(blob.Info) error) error {
	s.lists++
	return s.Store.List(ctx, prefix, after, fn)
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

// TestReconcileOwner checks that the orphan reconciliation deletes blobs only
// in a store that its database owns: it does nothing until first-run setup
// has completed, as setup may yet choose another store, and then claims the
// store for its database; once a copy of its database's files, which has its
// identity, claims the store with a token of its own, it deletes nothing,
// still claims the store, so that the copy finds its claim in turn, and goes
// on deleting nothing, although the store holds its own claim again, until
// the owner blob is deleted, as once the copy is gone; claims whose writes
// the store refused, however many in a row, leave the token before, which it
// takes as its own; a
// second database, as a copy of the first restored into a database of its
// own to try a new version on, which has the same workspaces, deletes none of
// the blobs that the first stores, which its own rows do not refer to, and
// says why, also when a workspace fails, whose job is then attempted again;
// and once the first has not claimed the store for longer than
// ownerTakeover, as when it is gone, the second takes the store over, and
// the first deletes nothing then.
func TestReconcileOwner(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	local, err := blob.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	workspaceID := ids.New()
	// reconciler returns the service of a new database that has the
	// workspace, whose store is dir until setup chooses one, and the
	// database.
	reconciler := func() (*Service, *dbtest.Database) {
		d := dbtest.New(t)
		if _, err := d.Admin(t).Exec(ctx, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Harbour', "+
			"'harbour', 'unlimited')", workspaceID); err != nil {
			t.Fatal(err)
		}
		db := d.Open(t)
		storage := NewStorage(db, StorageOptions{Dir: dir})
		t.Cleanup(func() { _ = storage.Close() })
		return &Service{db: db, storage: storage, log: slog.New(slog.DiscardHandler)}, d
	}
	// put stores the blob of the content whose hash ends in b, which no row
	// refers to.
	put := func(b byte) string {
		var sum [32]byte
		sum[31] = b
		key := blobKey(workspaceID, sum[:], "png")
		if err := local.Put(ctx, key, []byte("x"), MimePNG); err != nil {
			t.Fatal(err)
		}
		age(t, dir, key)
		return key
	}
	stored := func(key string) bool {
		_, err := local.Stat(ctx, key)
		return err == nil
	}
	owner := func() string {
		data, err := os.ReadFile(filepath.Join(dir, OwnerKey))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	first, d := reconciler()
	orphan := put(1)
	if n, err := first.Reconcile(ctx); n != 0 || err != nil || !stored(orphan) || stored(OwnerKey) {
		t.Errorf("before setup, the reconciliation deleted %d blobs, and claimed the store %t: %v; want none, "+
			"and nothing claimed", n, stored(OwnerKey), err)
	}
	d.MarkSetUp(t)
	if n, err := first.Reconcile(ctx); n != 1 || err != nil || stored(orphan) || !stored(OwnerKey) {
		t.Fatalf("after setup, the reconciliation deleted %d blobs, and claimed the store %t: %v; want 1, and the "+
			"store claimed", n, stored(OwnerKey), err)
	}
	self, _, _ := strings.Cut(owner(), " ")

	write := func(content string) {
		if err := local.Put(ctx, OwnerKey, []byte(content), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	orphan = put(5)
	write(self + " copy\n")
	for i := range 2 {
		n, err := first.Reconcile(ctx)
		if oe, ok := errors.AsType[*OwnerError](err); n != 0 || !ok || !oe.Copy || !stored(orphan) ||
			!errors.As(err, new(*river.JobCancelError)) || strings.HasSuffix(owner(), " copy\n") {
			t.Errorf("run %d once a copy claimed the store deleted %d blobs, and claimed the store %t: %v; want "+
				"none, the store claimed, and a cancelled OwnerError of a copy", i, n,
				!strings.HasSuffix(owner(), " copy\n"), err)
		}
	}
	if err := local.Delete(ctx, OwnerKey); err != nil {
		t.Fatal(err)
	}
	if n, err := first.Reconcile(ctx); n != 1 || err != nil || stored(orphan) {
		t.Errorf("once the owner blob was deleted, the reconciliation deleted %d blobs: %v; want 1", n, err)
	}
	// The store refuses the writes of two claims in a row, as when its disk
	// is full, and then takes the next.
	before := owner()
	for range 2 {
		if _, err := first.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		write(before)
	}
	orphan = put(6)
	if n, err := first.Reconcile(ctx); n != 1 || err != nil || stored(orphan) {
		t.Errorf("after two claims in a row whose writes the store refused, the reconciliation deleted %d blobs: "+
			"%v; want 1", n, err)
	}
	// An owner blob that names no database, as one that an operator emptied
	// rather than deleted, claims the store for none.
	write("")
	orphan = put(7)
	if n, err := first.Reconcile(ctx); n != 1 || err != nil || stored(orphan) || !strings.HasPrefix(owner(),
		self+" ") {
		t.Errorf("once the owner blob was emptied, the reconciliation deleted %d blobs, and claimed the store "+
			"%t: %v; want 1, and the store claimed", n, strings.HasPrefix(owner(), self+" "), err)
	}
	claimed := owner()

	second, d2 := reconciler()
	d2.MarkSetUp(t)
	// A file that the first stores after the copy was made, which the
	// second's rows do not refer to.
	orphan = put(2)
	n, err := second.Reconcile(ctx)
	if oe, ok := errors.AsType[*OwnerError](err); n != 0 || !ok || oe.Owner != self || oe.Copy || !stored(orphan) ||
		owner() != claimed || !errors.As(err, new(*river.JobCancelError)) {
		t.Errorf("the second database's reconciliation deleted %d blobs, and claimed the store %t: %v; want none, "+
			"and a cancelled OwnerError of the first", n, owner() != claimed, err)
	}
	// A workspace whose blobs the store does not list, as a file stands where
	// their directory would be.
	broken := ids.New()
	if _, err := d2.Admin(t).Exec(ctx, "INSERT INTO workspace (id, name, slug, plan_key) VALUES ($1, 'Broken', "+
		"'broken', 'unlimited')", broken); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, broken.String()), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	n, err = second.Reconcile(ctx)
	if n != 0 || !errors.As(err, new(*OwnerError)) || errors.As(err, new(*river.JobCancelError)) || !stored(orphan) {
		t.Errorf("the second database's reconciliation, with a workspace that fails, deleted %d blobs: %v; want "+
			"none, and an OwnerError of a job attempted again", n, err)
	}
	if err := os.Remove(filepath.Join(dir, broken.String())); err != nil {
		t.Fatal(err)
	}

	// The first has not claimed the store for longer than ownerTakeover.
	long := time.Now().Add(-ownerTakeover - time.Hour)
	if err := os.Chtimes(filepath.Join(dir, OwnerKey), long, long); err != nil {
		t.Fatal(err)
	}
	if n, err := second.Reconcile(ctx); n != 1 || err != nil || stored(orphan) || owner() == claimed {
		t.Errorf("the second database's reconciliation, once the first's claim is old, deleted %d blobs, and "+
			"claimed the store %t: %v; want 1, and the store claimed", n, owner() != claimed, err)
	}
	orphan = put(3)
	if n, err := first.Reconcile(ctx); n != 0 || !errors.As(err, new(*OwnerError)) || !stored(orphan) {
		t.Errorf("the first database's reconciliation, once the second claimed the store, deleted %d blobs: %v; "+
			"want none, and an OwnerError", n, err)
	}
}

// TestFinalStore checks that the store is final, which an upload needs,
// once first-run setup has completed, or at once if the configuration names
// it, and that before then the default store is returned, but is not final,
// and that asking for the final one opens none.
func TestFinalStore(t *testing.T) {
	ctx := context.Background()
	// noWorkspace reads an instance whose setup is pending, and that names
	// no bucket.
	dir := filepath.Join(t.TempDir(), "assets")
	pending := NewStorage(noWorkspace{}, StorageOptions{Dir: dir})
	t.Cleanup(func() { _ = pending.Close() })
	if _, err := pending.FinalStore(ctx); !errors.Is(err, ErrSetupRequired) {
		t.Errorf("the final store before setup: %v, want ErrSetupRequired", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("asking for the final store before setup made its directory: %v", err)
	}
	if _, err := pending.Store(ctx); err != nil {
		t.Errorf("the store before setup: %v", err)
	}
	fixed := NewStorage(noWorkspace{}, StorageOptions{Configured: &config.AssetStorage{Dir: t.TempDir()}})
	t.Cleanup(func() { _ = fixed.Close() })
	if _, err := fixed.FinalStore(ctx); err != nil {
		t.Errorf("the final store that the configuration names, before setup: %v", err)
	}
}
