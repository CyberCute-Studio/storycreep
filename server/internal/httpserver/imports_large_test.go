package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math/rand/v2"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/CyberCute-Studio/storycreep/server/internal/authoring"
	"github.com/CyberCute-Studio/storycreep/server/internal/fixture"
	"github.com/CyberCute-Studio/storycreep/server/internal/snapshot"
)

// largeImportEnv turns on TestImportLargeArchive, which takes many minutes and
// gigabytes, so that only a person who means to measure it runs it.
const largeImportEnv = "STORYCREEP_TEST_IMPORT_LARGE"

// noisePNG returns a PNG of about bytes bytes that does not compress, each
// different, as an upload of a photograph is.
func noisePNG(t *testing.T, r *rand.Rand, side int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		if i%4 == 3 {
			img.Pix[i] = 255
			continue
		}
		img.Pix[i] = byte(r.Uint32())
	}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestImportLargeArchive runs FR-DATA-01 AC4 at its size, where
// STORYCREEP_TEST_IMPORT_LARGE is set: the large fixture with 30 releases and
// 900 MB of assets, exported, uploaded in chunks with an interruption that it
// resumes from, and imported within the job's timeout, reporting its progress.
//
// Verifies: FR-DATA-01/AC4
func TestImportLargeArchive(t *testing.T) {
	if os.Getenv(largeImportEnv) == "" {
		t.Skipf("set %s to measure an import at the size of FR-DATA-01 AC4", largeImportEnv)
	}
	s, d, owner, ws := inviteServer(t, "open", nil)
	unlimited(s)
	openMaintenance(t, s, d)
	f, err := fixture.Generate(fixture.Large, 1)
	if err != nil {
		t.Fatal(err)
	}
	p := createProject(t, s.main, owner, ws, f.Name).ID
	start := time.Now()
	sender := fixture.Sender{Client: &http.Client{Transport: handlerTransport{s.main}},
		Endpoint: "http://localhost:8080" + commandsPath(p), Header: owner.header(true)}
	if err := sender.Send(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded the large fixture in %v", time.Since(start).Round(time.Second))

	start = time.Now()
	r := rand.New(rand.NewPCG(1, 84))
	// An opaque image is written as RGB, so each is about 10.3 MB.
	const assets, side = 90, 1850
	for range assets {
		upload(t, s.main, owner, p, noisePNG(t, r, side), http.StatusCreated)
	}
	t.Logf("uploaded %d assets in %v", assets, time.Since(start).Round(time.Second))

	startWorkers(t, s, d)
	start = time.Now()
	for n := range 30 {
		report := validationReport(t, s.main, owner, p)
		rel := snapshotFrom(t, call(s.main, http.MethodPost, snapshotsPath(p), publishBody(t,
			fmt.Sprintf("1.%d.0", n), report), owner.header(true)), http.StatusAccepted)
		for deadline := time.Now().Add(10 * time.Minute); ; time.Sleep(200 * time.Millisecond) {
			got := snapshotFrom(t, call(s.main, http.MethodGet, snapshotsPath(p)+"/"+rel.ID, nil, owner.header(false)),
				http.StatusOK)
			if got.Status == snapshot.Ready {
				break
			}
			if got.Status == snapshot.Failed || time.Now().After(deadline) {
				t.Fatalf("release %d ended as %+v", n, got)
			}
		}
	}
	t.Logf("published 30 releases in %v", time.Since(start).Round(time.Second))

	start = time.Now()
	e := askExport(t, s.main, owner, p, map[string]any{})
	for deadline := time.Now().Add(50 * time.Minute); ; time.Sleep(time.Second) {
		res := call(s.main, http.MethodGet, authoringExportsPath(p)+"/"+e.ID, nil, owner.header(false))
		if err := json.Unmarshal(res.body, &e); err != nil {
			t.Fatal(err)
		}
		if e.Status == authoring.StatusReady {
			break
		}
		if e.Status == authoring.StatusFailed || time.Now().After(deadline) {
			t.Fatalf("the export ended as %+v", e)
		}
	}
	res := call(s.main, http.MethodGet, authoringExportsPath(p)+"/"+e.ID+"/download", nil, owner.header(false))
	if res.status != http.StatusOK {
		t.Fatalf("download: %d", res.status)
	}
	archive := res.body
	t.Logf("exported an archive of %d MB in %v", len(archive)/1_000_000, time.Since(start).Round(time.Second))
	if len(archive) < 900_000_000 {
		t.Fatalf("the archive has only %d MB", len(archive)/1_000_000)
	}

	start = time.Now()
	i := beginImport(t, s.main, owner, ws, len(archive), "")
	// An upload that is cut off halfway, and resumes from what the server lists.
	half := make([]int, 0, i.ChunkCount/2)
	for n := 0; n < i.ChunkCount/2; n++ {
		half = append(half, n)
	}
	putChunks(t, s.main, owner, ws, i, archive, half...)
	seen := readImport(t, s.main, owner, ws, i.ID)
	if len(seen.Received) != len(half) {
		t.Fatalf("after the interruption the server lists %d chunks, want %d", len(seen.Received), len(half))
	}
	var rest []int
	for n := i.ChunkCount / 2; n < i.ChunkCount; n++ {
		rest = append(rest, n)
	}
	putChunks(t, s.main, owner, ws, i, archive, rest...)
	t.Logf("uploaded %d chunks in %v", i.ChunkCount, time.Since(start).Round(time.Second))
	start = time.Now()
	if res := call(s.main, http.MethodPost, importsPath(ws)+"/"+i.ID+"/complete", nil, owner.header(true)); res.status !=
		http.StatusAccepted {
		t.Fatalf("complete: %d %s", res.status, res.body)
	}
	phases := map[string]bool{}
	for deadline := time.Now().Add(50 * time.Minute); ; time.Sleep(time.Second) {
		got := readImport(t, s.main, owner, ws, i.ID)
		if !phases[got.Phase] {
			phases[got.Phase] = true
			t.Logf("%v: %s", time.Since(start).Round(time.Second), got.Phase)
		}
		if got.Status == authoring.ImportReady {
			t.Logf("imported in %v: %+v", time.Since(start).Round(time.Second), *got.Report)
			if got.Report.Releases != 30 || got.Report.Assets != assets {
				t.Errorf("the report is %+v", *got.Report)
			}
			return
		}
		if got.Status == authoring.ImportFailed || time.Now().After(deadline) {
			t.Fatalf("the import ended as %s: %+v", got.Status, got.Failure)
		}
	}
}
