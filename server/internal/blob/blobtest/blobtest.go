// Package blobtest is the test suite that both drivers of package blob pass
// (PRD FR-ASSET-01 AC1, ADR-009), and the S3-compatible object storage that
// the tests of the S3 driver run against: MinIO, in Docker, which
// testcontainers starts once per test binary, with a bucket of its own for
// each test.
//
// The tests that use MinIO need Docker. With go test -short they are
// skipped, which is how CI runs the suite on the runners that cannot run
// Linux containers; a Linux job runs them (PRD §9.9). Without -short, a
// missing Docker fails the test, as package dbtest does, so that a broken
// Docker never passes for a passing suite. A test binary that uses MinIO
// calls Stop from its TestMain once its tests are done, which removes the
// container where testcontainers' reaper does not run, as with Podman.
//
// One environment variable configures it. Only tests import this package,
// so it is not part of the server, which reads its configuration only
// through package config.
//
//	STORYCREEP_TEST_MINIO_IMAGE  The image to run. The default is
//	                             DefaultMinIOImage.
package blobtest

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/CyberCute-Studio/storycreep/server/internal/blob"
	"github.com/CyberCute-Studio/storycreep/server/internal/config"
	"github.com/CyberCute-Studio/storycreep/server/internal/secret"
)

// ImageVariable names the environment variable that picks the image of
// MinIO.
const ImageVariable = "STORYCREEP_TEST_MINIO_IMAGE"

// DefaultMinIOImage is the image that runs when ImageVariable is not set:
// Pigsty's build of MinIO's community edition, whose upstream no longer
// publishes images, pinned to a release, for linux/amd64 and linux/arm64.
const DefaultMinIOImage = "pgsty/minio:RELEASE.2026-08-04T00-00-00Z"

// Image returns the image of MinIO that the tests run.
func Image() string {
	if image := os.Getenv(ImageVariable); image != "" {
		return image
	}
	return DefaultMinIOImage
}

// Suite runs the tests that every Store passes, on store, which is empty,
// as subtests of t. Each subtest writes the keys of a workspace of its own,
// so that they do not see each other's blobs.
func Suite(t *testing.T, store blob.Store) {
	t.Helper()
	ctx := context.Background()
	workspace := func() string { return strings.ToLower(rand.Text()) }

	t.Run("put and open", func(t *testing.T) {
		key := workspace() + "/" + strings.Repeat("ab", 32) + ".png"
		data := []byte("\x89PNG not really")
		if err := store.Put(ctx, key, data, "image/png"); err != nil {
			t.Fatal(err)
		}
		if got := read(t, store, key); !bytes.Equal(got, data) {
			t.Errorf("the blob holds %q, want %q", got, data)
		}
		info, err := store.Stat(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if info.Key != key || info.Size != int64(len(data)) || time.Since(info.Modified) > time.Hour ||
			time.Until(info.Modified) > time.Hour {
			t.Errorf("Stat = %+v, want key %s and size %d, modified now", info, key, len(data))
		}
	})

	t.Run("put replaces", func(t *testing.T) {
		key := workspace() + "/blob.gif"
		for _, data := range []string{"first", "second, longer"} {
			if err := store.Put(ctx, key, []byte(data), "image/gif"); err != nil {
				t.Fatal(err)
			}
			if got := string(read(t, store, key)); got != data {
				t.Errorf("the blob holds %q, want %q", got, data)
			}
		}
		if keys := list(t, store, path(key)); !slices.Equal(keys, []string{key}) {
			t.Errorf("after two puts, the prefix lists %q, want the key alone", keys)
		}
	})

	t.Run("an empty blob", func(t *testing.T) {
		key := workspace() + "/empty.svg"
		if err := store.Put(ctx, key, nil, "image/svg+xml"); err != nil {
			t.Fatal(err)
		}
		if got := read(t, store, key); len(got) != 0 {
			t.Errorf("the blob holds %q", got)
		}
	})

	t.Run("missing", func(t *testing.T) {
		key := workspace() + "/missing.png"
		if _, _, err := store.Open(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			t.Errorf("Open: %v, want ErrNotFound", err)
		}
		if _, err := store.Stat(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			t.Errorf("Stat: %v, want ErrNotFound", err)
		}
		if err := store.Delete(ctx, key); err != nil {
			t.Errorf("deleting a blob that does not exist: %v", err)
		}
		if keys := list(t, store, workspace()+"/"); len(keys) != 0 {
			t.Errorf("a prefix without blobs lists %q", keys)
		}
	})

	t.Run("delete", func(t *testing.T) {
		key := workspace() + "/doomed.webp"
		if err := store.Put(ctx, key, []byte("RIFF"), "image/webp"); err != nil {
			t.Fatal(err)
		}
		if err := store.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Stat(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			t.Errorf("Stat after Delete: %v, want ErrNotFound", err)
		}
		if err := store.Delete(ctx, key); err != nil {
			t.Errorf("deleting it again: %v", err)
		}
	})

	t.Run("list a workspace", func(t *testing.T) {
		mine, theirs := workspace(), workspace()
		want := []string{mine + "/a.png", mine + "/b.thumb.png", mine + "/c.jpg"}
		for _, key := range append(slices.Clone(want), theirs+"/a.png") {
			if err := store.Put(ctx, key, []byte(key), "application/octet-stream"); err != nil {
				t.Fatal(err)
			}
		}
		if keys := list(t, store, mine+"/"); !slices.Equal(keys, want) {
			t.Errorf("the workspace lists %q, want %q in order", keys, want)
		}
		sizes := map[string]int64{}
		if err := store.List(ctx, mine+"/", "", func(i blob.Info) error {
			sizes[i.Key] = i.Size
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, key := range want {
			if sizes[key] != int64(len(key)) {
				t.Errorf("%s lists with size %d, want %d", key, sizes[key], len(key))
			}
		}
		// A key's segments sort as the key does, so a key under a directory
		// a follows one of a-1, and one of a.png, whose characters sort
		// before the slash.
		nested := workspace()
		ordered := []string{nested + "/a-1/x.png", nested + "/a.png", nested + "/a/x.png", nested + "/b/c/d.png"}
		for _, i := range []int{2, 0, 3, 1} {
			if err := store.Put(ctx, ordered[i], []byte("x"), "image/png"); err != nil {
				t.Fatal(err)
			}
		}
		if keys := list(t, store, nested+"/"); !slices.Equal(keys, ordered) {
			t.Errorf("the nested keys list %q, want %q in order", keys, ordered)
		}
		// A listing after a key, or any string, lists the keys that sort after
		// it, in a directory too, and none of a directory whose keys all sort
		// before it.
		for _, tt := range []struct {
			prefix, after string
			want          []string
		}{
			{mine + "/", mine + "/a.png", want[1:]},
			{mine + "/", mine + "/b", want[1:]},
			{mine + "/", mine + "/c.jpg", nil},
			{mine + "/", "", want},
			{nested + "/", nested + "/a-1/x.png", ordered[1:]},
			{nested + "/", nested + "/a/", ordered[2:]},
			{nested + "/", nested + "/a/x.png", ordered[3:]},
			{nested + "/", nested + "/b/c", ordered[3:]},
		} {
			if keys := listAfter(t, store, tt.prefix, tt.after); !slices.Equal(keys, tt.want) {
				t.Errorf("the keys of %s after %q list %q, want %q", tt.prefix, tt.after, keys, tt.want)
			}
		}
		stop := errors.New("stop")
		calls := 0
		err := store.List(ctx, mine+"/", "", func(blob.Info) error {
			calls++
			return stop
		})
		if !errors.Is(err, stop) || calls != 1 {
			t.Errorf("an error of fn: List returned %v after %d calls, want it after one", err, calls)
		}
	})

	t.Run("keys", func(t *testing.T) {
		for _, key := range []string{"", "../escape.png", "a/../../escape.png", "/absolute.png", "a//b.png",
			"a/.hidden.png", ".put-x", "a\\b.png", "a/b c.png", "a/b.png/", "a/\x00.png", "a/č.png"} {
			if err := store.Put(ctx, key, []byte("x"), "image/png"); err == nil {
				t.Errorf("Put takes the key %q", key)
			}
			if _, _, err := store.Open(ctx, key); err == nil || errors.Is(err, blob.ErrNotFound) {
				t.Errorf("Open of the key %q: %v, want an error that is not ErrNotFound", key, err)
			}
		}
		for _, prefix := range []string{"a", "../", "a/../"} {
			if err := store.List(ctx, prefix, "", func(blob.Info) error { return nil }); err == nil {
				t.Errorf("List takes the prefix %q", prefix)
			}
		}
	})

	t.Run("signed URL", func(t *testing.T) {
		key := workspace() + "/signed.svg"
		data := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
		if err := store.Put(ctx, key, data, "image/svg+xml"); err != nil {
			t.Fatal(err)
		}
		u, ok, err := store.SignedURL(ctx, key, time.Minute, "image/svg+xml", `inline; filename="x.svg"`)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			if u != "" {
				t.Errorf("a store without URLs returned %q", u)
			}
			return
		}
		res, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		got, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK || !bytes.Equal(got, data) ||
			res.Header.Get("Content-Type") != "image/svg+xml" ||
			res.Header.Get("Content-Disposition") != `inline; filename="x.svg"` ||
			res.Header.Get("Cache-Control") != "private, max-age=60" {
			t.Errorf("the signed URL answers %d %s, %q, %q, %q", res.StatusCode, got, res.Header.Get("Content-Type"),
				res.Header.Get("Content-Disposition"), res.Header.Get("Cache-Control"))
		}
		// The URL is signed for the key: another key's is refused.
		other := strings.Replace(u, "/signed.svg", "/other.svg", 1)
		if res, err := http.Get(other); err != nil {
			t.Fatal(err)
		} else if res.Body.Close(); res.StatusCode != http.StatusForbidden {
			t.Errorf("the signature taken to another key: %d, want 403", res.StatusCode)
		}
	})
}

// path returns the prefix of key's first segment.
func path(key string) string {
	first, _, _ := strings.Cut(key, "/")
	return first + "/"
}

// read returns the blob of key.
func read(t *testing.T, store blob.Store, key string) []byte {
	t.Helper()
	r, info, err := store.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(data)) || info.Key != key {
		t.Errorf("Open's Info is %+v for %d bytes of %s", info, len(data), key)
	}
	return data
}

// list returns the keys that store lists under prefix.
func list(t *testing.T, store blob.Store, prefix string) []string {
	t.Helper()
	return listAfter(t, store, prefix, "")
}

// listAfter returns the keys that store lists under prefix after after.
func listAfter(t *testing.T, store blob.Store, prefix, after string) []string {
	t.Helper()
	var keys []string
	if err := store.List(context.Background(), prefix, after, func(i blob.Info) error {
		keys = append(keys, i.Key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return keys
}

// minio is the shared MinIO server.
type minio struct {
	container testcontainers.Container
	host      string
	port      int
	user      string
	password  string
}

var shared struct {
	once sync.Once
	srv  *minio
	err  error
}

// S3 returns the settings of a new, empty bucket of the shared MinIO
// server, which it starts on first use. It skips the test with -short.
func S3(t testing.TB) config.S3 {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped with -short: needs MinIO, which the tests run in Docker")
	}
	shared.once.Do(func() {
		shared.srv, shared.err = start(context.Background())
	})
	if shared.err != nil {
		t.Fatal(shared.err)
	}
	m := shared.srv
	cfg := config.S3{Host: m.host, Port: m.port, Security: config.S3None, Region: config.DefaultS3Region,
		Bucket: "test-" + strings.ToLower(rand.Text()), AccessKeyID: m.user, SecretAccessKey: secret.New(m.password)}
	client := s3.NewFromConfig(aws.Config{Region: cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(m.user, m.password, "")}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint())
		o.UsePathStyle = true
	})
	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		t.Fatalf("create the bucket %s: %v", cfg.Bucket, err)
	}
	return cfg
}

// start starts MinIO.
func start(ctx context.Context) (_ *minio, err error) {
	m := &minio{user: "storycreep", password: rand.Text()}
	c, err := testcontainers.Run(ctx, Image(),
		testcontainers.WithCmd("server", "/data"),
		testcontainers.WithEnv(map[string]string{"MINIO_ROOT_USER": m.user, "MINIO_ROOT_PASSWORD": m.password}),
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp").
			WithStartupTimeout(2*time.Minute)),
	)
	// Run may return a container together with its error.
	if c != nil {
		m.container = c
	}
	defer func() {
		if err != nil && m.container != nil {
			_ = testcontainers.TerminateContainer(m.container)
		}
	}()
	if err != nil {
		return nil, fmt.Errorf("start MinIO (%s): %w\n"+
			"The tests of the S3 driver run MinIO in Docker. Start Docker, or run go test -short to skip them.",
			Image(), err)
	}
	if m.host, err = m.container.Host(ctx); err != nil {
		return nil, err
	}
	port, err := m.container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		return nil, err
	}
	if m.port, err = strconv.Atoi(port.Port()); err != nil {
		return nil, err
	}
	return m, nil
}

// Stop removes the shared MinIO container, if the tests started it, for a
// test binary's TestMain once its tests are done.
func Stop() {
	if m := shared.srv; m != nil {
		if err := testcontainers.TerminateContainer(m.container); err != nil {
			fmt.Fprintf(os.Stderr, "blobtest: stop MinIO: %v\n", err)
		}
	}
}
