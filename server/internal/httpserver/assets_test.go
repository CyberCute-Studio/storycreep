package httpserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/CyberCute-Studio/storycreep/server/internal/api"
	"github.com/CyberCute-Studio/storycreep/server/internal/asset"
	"github.com/CyberCute-Studio/storycreep/server/internal/blob/blobtest"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
	"github.com/CyberCute-Studio/storycreep/server/internal/usage"
)

// assetPath returns the path of the asset of the project, or of the
// project's assets if asset is "".
func assetPath(project, asset string) string {
	p := projectPath(project) + "/assets"
	if asset != "" {
		p += "/" + asset
	}
	return p
}

// testPNG returns a PNG image of width × height pixels, whose colours
// depend on its size, so that images of two sizes differ.
func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.NRGBA{R: uint8(x * 255 / width), G: uint8(y * 255 / height), B: uint8(width),
				A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// uploadFile uploads data, sent with the Content-Type contentType, to the
// project, as si.
func uploadFile(h http.Handler, si signedIn, project string, data []byte, contentType string) response {
	hdr := si.header(true)
	maps.Copy(hdr, newClient())
	return send(h, http.MethodPost, assetPath(project, ""), data, contentType, hdr)
}

// upload uploads data to the project as si, checks that it is answered with
// want, and returns the asset.
func upload(t *testing.T, h http.Handler, si signedIn, project string, data []byte, want int) asset.Asset {
	t.Helper()
	res := uploadFile(h, si, project, data, "application/octet-stream")
	if res.status != want {
		t.Fatalf("upload: %d %s, want %d", res.status, res.body, want)
	}
	var a asset.Asset
	if err := json.Unmarshal(res.body, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

// TestUploadAsset checks an upload and every read of the asset: the asset's
// fields, its file as it was uploaded, served with the headers that keep it
// from running in the application's origin, a Cache-Control that lets the
// browser keep it for 5 minutes, and an ETag that revalidates it, and its
// thumbnail, which a small image is its own of. Uploading the same
// file to the project again answers with the asset that it has, and to
// another project of the workspace with an asset of its own that shares the
// blob, which counts once towards the workspace's storage.
//
// Verifies: FR-ASSET-02/AC3
func TestUploadAsset(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	data := testPNG(t, 40, 30)
	res := uploadFile(s.main, owner, p, data, "text/plain")
	if res.status != http.StatusCreated {
		t.Fatalf("upload: %d %s", res.status, res.body)
	}
	var a asset.Asset
	if err := json.Unmarshal(res.body, &a); err != nil {
		t.Fatal(err)
	}
	if a.ProjectID != p || a.ContentType != "image/png" || a.Bytes != int64(len(data)) || a.Width != 40 ||
		a.Height != 30 || len(a.SHA256) != 64 || a.UploadedBy != owner.body.UserID ||
		!strings.HasPrefix(a.ID, "ast_") {
		t.Errorf("the asset is %+v", a)
	}
	if loc := res.header.Get("Location"); loc != assetPath(p, a.ID) {
		t.Errorf("Location: %q, want %q", loc, assetPath(p, a.ID))
	}

	got := call(s.main, http.MethodGet, assetPath(p, a.ID), nil, owner.header(false))
	var read asset.Asset
	if got.status != http.StatusOK || json.Unmarshal(got.body, &read) != nil || read != a {
		t.Errorf("read the asset: %d %s, want %+v", got.status, got.body, a)
	}

	for _, path := range []string{"/content", "/thumbnail"} {
		file := call(s.main, http.MethodGet, assetPath(p, a.ID)+path, nil, owner.header(false))
		h := file.header
		if file.status != http.StatusOK || !bytes.Equal(file.body, data) {
			t.Fatalf("%s: %d, %d bytes, want the file", path, file.status, len(file.body))
		}
		for name, want := range map[string]string{
			"Content-Type":            "image/png",
			"Content-Security-Policy": "sandbox",
			"X-Content-Type-Options":  "nosniff",
			"Content-Disposition":     `inline; filename="` + a.ID + `.png"`,
			"Cache-Control":           "private, max-age=300",
			"Content-Length":          fmt.Sprint(len(data)),
		} {
			if h.Get(name) != want {
				t.Errorf("%s: %s is %q, want %q", path, name, h.Get(name), want)
			}
		}
		etag := h.Get("ETag")
		hdr := owner.header(false)
		hdr.Set("If-None-Match", etag)
		// The 304 sends the same Cache-Control, which replaces the stored
		// response's.
		if again := call(s.main, http.MethodGet, assetPath(p, a.ID)+path, nil, hdr); etag == "" ||
			again.status != http.StatusNotModified || len(again.body) != 0 ||
			again.header.Get("Cache-Control") != "private, max-age=300" {
			t.Errorf("%s with If-None-Match %q: %d %s, Cache-Control %q; want 304", path, etag, again.status,
				again.body, again.header.Get("Cache-Control"))
		}
		// HEAD is answered with the headers of GET, without the file, which
		// the route then does not read.
		if head := call(s.main, http.MethodHead, assetPath(p, a.ID)+path, nil, owner.header(false)); head.status !=
			http.StatusOK || len(head.body) != 0 || head.header.Get("Content-Length") != fmt.Sprint(len(data)) {
			t.Errorf("HEAD %s: %d, %d bytes, Content-Length %q; want 200 and no file", path, head.status,
				len(head.body), head.header.Get("Content-Length"))
		}
	}

	if same := upload(t, s.main, owner, p, data, http.StatusOK); same != a {
		t.Errorf("the same file again: %+v, want %+v", same, a)
	}
	other := createProject(t, s.main, owner, ws, "Harbour").ID
	b := upload(t, s.main, owner, other, data, http.StatusCreated)
	if b.ID == a.ID || b.SHA256 != a.SHA256 || b.ProjectID != other {
		t.Errorf("the same file in another project: %+v", b)
	}
	if n := countRows(t, d, `SELECT count(DISTINCT storage_key) FROM asset WHERE workspace_id = $1::uuid`,
		decode(t, ids.Workspace, ws).String()); n != 1 {
		t.Errorf("the workspace has %d blobs, want 1", n)
	}
	if used := readUsage(t, s.main, owner, ws)["storage_bytes"]["used"]; used != float64(len(data)) {
		t.Errorf("the workspace uses %v bytes, want %d", used, len(data))
	}
	// An asset of another project is not this project's.
	if res := call(s.main, http.MethodGet, assetPath(p, b.ID), nil, owner.header(false)); res.status !=
		http.StatusNotFound {
		t.Errorf("another project's asset: %d, want 404", res.status)
	}
}

// pngWithSize returns a PNG file whose header says that it is width ×
// height pixels, with a valid checksum, and no image data after it.
func pngWithSize(width, height uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], width)
	binary.BigEndian.PutUint32(ihdr[4:], height)
	ihdr[8], ihdr[9] = 8, 6
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

// TestUploadRefusesNonImages checks that a file whose content is not an
// image that an upload takes is refused with 422 image_invalid, whatever its
// Content-Type says, and stores nothing: text sent as a PNG image, a file
// that starts as a PNG image does but whose header does not decode, a PDF,
// and an image of more pixels than an upload takes.
//
// Verifies: FR-ASSET-02/AC1
func TestUploadRefusesNonImages(t *testing.T) {
	t.Parallel()
	s, d, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	for _, tt := range []struct {
		name        string
		data        []byte
		contentType string
		reason      string
	}{
		{"text named as a PNG image", []byte("This is not an image, whatever its name says."), "image/png",
			asset.ReasonType},
		{"HTML", []byte("<html><script>alert(1)</script></html>"), "image/svg+xml", asset.ReasonType},
		{"a PDF", []byte("%PDF-1.7\n1 0 obj\n"), "application/pdf", asset.ReasonType},
		{"a broken PNG header", []byte("\x89PNG\r\n\x1a\nnot a chunk"), "image/png", asset.ReasonMalformed},
		{"a GIF header alone", []byte("GIF89a"), "image/gif", asset.ReasonMalformed},
		{"too many pixels", pngWithSize(10000, 10000), "image/png", asset.ReasonDimensions},
		{"too wide", pngWithSize(20000, 1), "image/png", asset.ReasonDimensions},
		{"an SVG that is not well-formed", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><g></svg>`),
			"image/svg+xml", asset.ReasonMalformed},
		{"XML that is not SVG", []byte(`<html xmlns="http://www.w3.org/1999/xhtml"/>`), "image/svg+xml",
			asset.ReasonType},
	} {
		res := uploadFile(s.main, owner, p, tt.data, tt.contentType)
		wantCode(t, tt.name, res, http.StatusUnprocessableEntity, api.CodeImageInvalid)
		if got := res.details(t)["reason"]; got != tt.reason {
			t.Errorf("%s: the reason is %v, want %s", tt.name, got, tt.reason)
		}
	}
	if n := countRows(t, d, "SELECT count(*) FROM asset"); n != 0 {
		t.Errorf("the refused uploads added %d assets", n)
	}
}

// TestUploadSanitizesSVG checks that an SVG image that holds a script, an
// event handler, and references outside itself is stored without them, and
// is served so, never as it was uploaded, while what draws the image stays.
//
// Verifies: FR-ASSET-02/AC2
func TestUploadSanitizesSVG(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	svg := []byte(`<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="120" height="80"
     onload="alert('load')">
  <script>alert('script')</script>
  <defs><linearGradient id="g"><stop offset="0" stop-color="#123456"/></linearGradient></defs>
  <rect width="120" height="80" fill="url(#g)" onclick="alert('click')"/>
  <image href="https://evil.example.com/track.png" width="10" height="10"/>
  <use xlink:href="https://evil.example.com/sprite.svg#a"/>
  <a href="javascript:alert('link')"><text x="5" y="20">Lighthouse</text></a>
  <foreignObject><div xmlns="http://www.w3.org/1999/xhtml"><script>alert('html')</script></div></foreignObject>
  <style>@import url(https://evil.example.com/x.css);</style>
  <circle r="5" style="fill:url(https://evil.example.com/p.svg)"/>
</svg>`)
	a := upload(t, s.main, owner, p, svg, http.StatusCreated)
	if a.ContentType != "image/svg+xml" || a.Width != 120 || a.Height != 80 {
		t.Errorf("the asset is %+v", a)
	}
	file := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/content", nil, owner.header(false))
	if file.status != http.StatusOK || file.header.Get("Content-Type") != "image/svg+xml" ||
		file.header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("the file: %d %v", file.status, file.header)
	}
	stored := string(file.body)
	if stored == string(svg) {
		t.Fatal("the SVG is served as it was uploaded")
	}
	for _, gone := range []string{"<script", "alert", "onload", "onclick", "evil.example.com", "javascript:",
		"foreignObject", "@import"} {
		if strings.Contains(stored, gone) {
			t.Errorf("the stored SVG still holds %q:\n%s", gone, stored)
		}
	}
	for _, kept := range []string{`<rect width="120" height="80" fill="url(#g)">`, "Lighthouse", `stop-color="#123456"`,
		`xmlns="http://www.w3.org/2000/svg"`} {
		if !strings.Contains(stored, kept) {
			t.Errorf("the stored SVG lost %q:\n%s", kept, stored)
		}
	}
	if a.Bytes != int64(len(file.body)) {
		t.Errorf("the asset has %d bytes, and its file %d", a.Bytes, len(file.body))
	}
}

// TestStorageLimit checks the one site that enforces storage_bytes: an
// upload of a file that the workspace does not store yet, which would take
// it past the cap of STORYCREEP_WORKSPACE_LIMITS, is refused with 413
// storage_limit_exceeded, with the workspace's usage and limit, and stores
// nothing, while a file that it stores already, in another project, takes up
// no more, and is taken.
//
// Verifies: FR-ASSET-02/AC4, FR-ENT-01/AC1, FR-ENT-03/AC1
func TestStorageLimit(t *testing.T) {
	t.Parallel()
	first, second := testPNG(t, 40, 40), testPNG(t, 41, 41)
	limit := len(first) + len(second)/2
	s, d, owner, ws := inviteServer(t, "open",
		map[string]string{"STORYCREEP_WORKSPACE_LIMITS": fmt.Sprintf("storage_bytes=%d", limit)})
	a := createProject(t, s.main, owner, ws, "A").ID
	b := createProject(t, s.main, owner, ws, "B").ID
	upload(t, s.main, owner, a, first, http.StatusCreated)

	res := uploadFile(s.main, owner, a, second, "image/png")
	wantCode(t, "a file past the limit", res, http.StatusRequestEntityTooLarge, api.CodeStorageLimitExceeded)
	if got := res.details(t); got["key"] != "storage_bytes" || got["usage"] != float64(len(first)) ||
		got["limit"] != float64(limit) || got["upgrade_url"] != nil {
		t.Errorf("details %v, want the storage limit of %d, with %d bytes used", got, limit, len(first))
	}
	if n := countRows(t, d, "SELECT count(*) FROM asset"); n != 1 {
		t.Errorf("the workspace has %d assets, want 1", n)
	}
	// The workspace stores the first file already.
	upload(t, s.main, owner, b, first, http.StatusCreated)
}

// TestUploadSizeLimit checks that a file larger than
// STORYCREEP_ASSET_MAX_BYTES is refused with 413 request_too_large, which
// names the limit, as is an SVG image that is not, but would be stored
// larger, as its characters are written as references once it is sanitized.
func TestUploadSizeLimit(t *testing.T) {
	t.Parallel()
	data := testPNG(t, 30, 30)
	maxBytes := len(data) - 1
	s, d, owner, ws := inviteServer(t, "open",
		map[string]string{"STORYCREEP_ASSET_MAX_BYTES": fmt.Sprint(maxBytes)})
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	res := uploadFile(s.main, owner, p, data, "image/png")
	wantCode(t, "a file too large", res, http.StatusRequestEntityTooLarge, api.CodeRequestTooLarge)
	if got := res.details(t)["max_bytes"]; got != float64(maxBytes) {
		t.Errorf("max_bytes is %v, want %d", got, maxBytes)
	}
	prefix, suffix := `<svg xmlns="http://www.w3.org/2000/svg"><text><![CDATA[`, `]]></text></svg>`
	svg := prefix + strings.Repeat("&", maxBytes-len(prefix)-len(suffix)) + suffix
	res = uploadFile(s.main, owner, p, []byte(svg), "image/svg+xml")
	wantCode(t, "an SVG image that would be stored too large", res, http.StatusRequestEntityTooLarge,
		api.CodeRequestTooLarge)
	if got := res.details(t)["max_bytes"]; got != float64(maxBytes) {
		t.Errorf("max_bytes is %v, want %d", got, maxBytes)
	}
	if n := countRows(t, d, "SELECT count(*) FROM asset"); n != 0 {
		t.Errorf("the workspace has %d assets, want none", n)
	}
	upload(t, s.main, owner, p, testPNG(t, 2, 2), http.StatusCreated)
}

// TestUploadToArchivedProject checks that an archived project, which is
// read-only, refuses an upload with 409 project_archived, and one in the
// trash with 404, before the file is read, while its assets stay readable
// while it is archived.
func TestUploadToArchivedProject(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	a := upload(t, s.main, owner, p, testPNG(t, 3, 3), http.StatusCreated)
	projectFrom(t, call(s.main, http.MethodPost, projectPath(p)+"/archive", nil, owner.header(true)), http.StatusOK)
	wantCode(t, "an archived project", uploadFile(s.main, owner, p, []byte("not even an image"), "image/png"),
		http.StatusConflict, api.CodeProjectArchived)
	if res := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/content", nil, owner.header(false)); res.status !=
		http.StatusOK {
		t.Errorf("read an archived project's asset: %d", res.status)
	}
	if res := call(s.main, http.MethodDelete, projectPath(p), nil, owner.header(true)); res.status !=
		http.StatusNoContent {
		t.Fatalf("delete the project: %d %s", res.status, res.body)
	}
	wantCode(t, "a project in the trash", uploadFile(s.main, owner, p, testPNG(t, 3, 3), "image/png"),
		http.StatusNotFound, api.CodeNotFound)
	wantCode(t, "an asset of a project in the trash", call(s.main, http.MethodGet, assetPath(p, a.ID)+"/content",
		nil, owner.header(false)), http.StatusNotFound, api.CodeNotFound)
}

// TestAssetOfAnotherWorkspace checks that no route reads another
// workspace's asset: neither through that workspace's project, on which the
// user has no role, nor through a project of the user's own, with the other
// asset's ID, however the request names it. The blob's key is never part of
// a URL, so guessing a content's hash reaches nothing.
//
// Verifies: FR-ASSET-01/AC2
func TestAssetOfAnotherWorkspace(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	theirs := createProject(t, s.main, owner, ws, "Theirs").ID
	secret := upload(t, s.main, owner, theirs, testPNG(t, 8, 8), http.StatusCreated)

	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	mine := createProject(t, s.main, jo, personalWorkspace(t, s.main, jo).ID, "Mine").ID
	// The same file is in Jo's workspace too, as a blob of its own.
	own := upload(t, s.main, jo, mine, testPNG(t, 8, 8), http.StatusCreated)
	if own.SHA256 != secret.SHA256 {
		t.Fatalf("the same file has two hashes: %s and %s", own.SHA256, secret.SHA256)
	}
	for _, path := range []string{
		assetPath(theirs, secret.ID), assetPath(theirs, secret.ID) + "/content",
		assetPath(theirs, secret.ID) + "/thumbnail", assetPath(mine, secret.ID),
		assetPath(mine, secret.ID) + "/content", assetPath(mine, secret.ID) + "/thumbnail",
	} {
		wantCode(t, "Jo reads "+path, call(s.main, http.MethodGet, path, nil, jo.header(false)),
			http.StatusNotFound, api.CodeNotFound)
	}
	wantCode(t, "Jo uploads to their project", uploadFile(s.main, jo, theirs, testPNG(t, 9, 9), "image/png"),
		http.StatusNotFound, api.CodeNotFound)
	// Paths that try to name a blob instead of an asset are not routes.
	for _, path := range []string{
		assetPath(mine, secret.SHA256) + "/content",
		assetPath(mine, "..%2F..%2F"+url.PathEscape(ws)+"%2F"+secret.SHA256+".png") + "/content",
	} {
		if res := call(s.main, http.MethodGet, path, nil, jo.header(false)); res.status < 400 ||
			bytes.Equal(res.body, testPNG(t, 8, 8)) {
			t.Errorf("Jo reads %s: %d", path, res.status)
		}
	}
}

// storedFiles returns the paths of the files under dir, the local driver's
// directory, relative to it.
func storedFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestThumbnailsAndOrphans checks the jobs of the assets: the thumbnail job
// makes a PNG thumbnail of a large image, which the thumbnail route serves
// once it is made, and the file until then, which a browser keeps for half a
// minute rather than 5, so that it soon asks again; and once no asset refers
// to a blob, as after its project is purged, the orphan reconciliation
// deletes it, and its thumbnail, but keeps a blob that another project's
// asset refers to, and usage's reconciliation corrects the storage that the
// purge left behind.
//
// Verifies: FR-ENT-02/AC1
func TestThumbnailsAndOrphans(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, d, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_ASSET_STORAGE": dir})
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	large, small := testPNG(t, 600, 300), testPNG(t, 7, 7)
	a := upload(t, s.main, owner, p, large, http.StatusCreated)
	upload(t, s.main, owner, p, small, http.StatusCreated)
	thumb := func() response {
		return call(s.main, http.MethodGet, assetPath(p, a.ID)+"/thumbnail", nil, owner.header(false))
	}
	// Until the thumbnail is made, the file is kept for half a minute, and a
	// 304 says so again, so that the browser soon asks for the thumbnail.
	pending := thumb()
	if pending.status != http.StatusOK || !bytes.Equal(pending.body, large) ||
		pending.header.Get("Cache-Control") != "private, max-age=30" {
		t.Errorf("the thumbnail before its job: %d, %d bytes, Cache-Control %q; want the file, kept for 30 s",
			pending.status, len(pending.body), pending.header.Get("Cache-Control"))
	}
	hdr := owner.header(false)
	hdr.Set("If-None-Match", pending.header.Get("ETag"))
	if again := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/thumbnail", nil, hdr); again.status !=
		http.StatusNotModified || again.header.Get("Cache-Control") != "private, max-age=30" {
		t.Errorf("the thumbnail before its job, revalidated: %d, Cache-Control %q; want 304, kept for 30 s",
			again.status, again.header.Get("Cache-Control"))
	}
	// The file itself is kept for as long as ever.
	if content := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/content", nil, owner.header(false)); content.
		header.Get("Cache-Control") != "private, max-age=300" {
		t.Errorf("the file while its thumbnail is not made: Cache-Control %q", content.header.Get("Cache-Control"))
	}
	workers := startWorkers(t, s, d)
	var cfg image.Config
	for deadline := time.Now().Add(30 * time.Second); ; {
		res := thumb()
		if !bytes.Equal(res.body, large) {
			var err error
			if cfg, err = png.DecodeConfig(bytes.NewReader(res.body)); err != nil {
				t.Fatalf("the thumbnail is not a PNG image: %v", err)
			}
			if res.header.Get("Content-Type") != "image/png" || res.header.Get("Content-Security-Policy") != "sandbox" ||
				res.header.Get("Cache-Control") != "private, max-age=300" {
				t.Errorf("the thumbnail's headers: %v", res.header)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the thumbnail job did not make the thumbnail")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if cfg.Width != asset.ThumbnailSide || cfg.Height != asset.ThumbnailSide/2 {
		t.Errorf("the thumbnail is %d × %d, want %d × %d", cfg.Width, cfg.Height, asset.ThumbnailSide,
			asset.ThumbnailSide/2)
	}
	if n := len(storedFiles(t, dir)); n != 3 {
		t.Errorf("the store holds %v, want the two files and a thumbnail", storedFiles(t, dir))
	}
	// Another project's asset of the large file shares its thumbnail at once,
	// without a job of its own.
	cove := createProject(t, s.main, owner, ws, "Cove").ID
	shared := upload(t, s.main, owner, cove, large, http.StatusCreated)
	res := call(s.main, http.MethodGet, assetPath(cove, shared.ID)+"/thumbnail", nil, owner.header(false))
	if cfg, err := png.DecodeConfig(bytes.NewReader(res.body)); err != nil || bytes.Equal(res.body, large) ||
		cfg.Width != asset.ThumbnailSide {
		t.Errorf("the thumbnail of the same file in another project: %d, %d bytes, %v; want the thumbnail",
			res.status, len(res.body), err)
	}
	if n := countRows(t, d, `SELECT count(*) FROM river_job WHERE kind = 'asset_thumbnail'`); n != 1 {
		t.Errorf("%d thumbnail jobs, want the first upload's alone", n)
	}

	// Another project shares the small file, whose blob stays.
	other := createProject(t, s.main, owner, ws, "Harbour").ID
	upload(t, s.main, owner, other, small, http.StatusCreated)
	// The workers stop before the purge, so that the reconciliation that
	// they run once they start, which a busy machine may leave until now,
	// does not run beside this test's.
	if err := workers.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The purge of a project deletes its assets by the cascade of their
	// foreign key, as purging it does.
	admin := d.Admin(t)
	if _, err := admin.Exec(context.Background(), "DELETE FROM project WHERE id = ANY($1)",
		[]ids.UUID{decode(t, ids.Project, p), decode(t, ids.Project, cove)}); err != nil {
		t.Fatal(err)
	}
	// A file that Put left half written, as when the server stopped, is not
	// a blob, and stays.
	if err := os.WriteFile(filepath.Join(dir, decode(t, ids.Workspace, ws).String(), ".put-unfinished"), nil,
		0o600); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.assets.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	files := storedFiles(t, dir)
	if deleted != 2 || len(files) != 2 || !strings.HasSuffix(files[1], ".png") || strings.Contains(files[1], "thumb") {
		t.Errorf("the reconciliation deleted %d blobs, and left %v; want the shared file and the unfinished one",
			deleted, files)
	}
	db := d.Open(t)
	if _, err := usage.Reconcile(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if used := readUsage(t, s.main, owner, ws)["storage_bytes"]["used"]; used != float64(len(small)) {
		t.Errorf("after reconciliation, the workspace uses %v bytes, want %d", used, len(small))
	}
}

// TestReconcileManyOrphans checks that the orphan reconciliation deletes in
// one run more blobs that no asset refers to than it deletes in one
// transaction, as a project of many images leaves once it is purged, and
// keeps the blob that an asset refers to, whichever batch it falls in.
func TestReconcileManyOrphans(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, _, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_ASSET_STORAGE": dir})
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	kept := upload(t, s.main, owner, p, testPNG(t, 7, 7), http.StatusCreated)
	wsDir := filepath.Join(dir, decode(t, ids.Workspace, ws).String())
	const orphans = 200
	for i := range orphans {
		name := fmt.Sprintf("%064x.png", i)
		if err := os.WriteFile(filepath.Join(wsDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := s.assets.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	files := storedFiles(t, dir)
	if deleted != orphans || len(files) != 1 || !strings.HasSuffix(files[0], kept.SHA256+".png") {
		t.Errorf("the reconciliation deleted %d blobs, and left %v; want %d deleted, and the asset's file left",
			deleted, files, orphans)
	}
}

// TestUndecodableThumbnail checks that an image whose header decodes, which
// an upload takes, but whose pixels do not, gets no thumbnail of its own:
// its job records it as its own thumbnail, which the route serves, and ends
// without another attempt, and an upload of the same file to another
// project shares that, rather than enqueue a job that would fail again.
func TestUndecodableThumbnail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, d, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_ASSET_STORAGE": dir})
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	large := testPNG(t, 600, 300)
	broken := large[:len(large)/2]
	a := upload(t, s.main, owner, p, broken, http.StatusCreated)
	startWorkers(t, s, d)
	for deadline := time.Now().Add(30 * time.Second); countRows(t, d,
		`SELECT count(*) FROM river_job WHERE kind = 'asset_thumbnail' AND state = 'cancelled'`) != 1; {
		if time.Now().After(deadline) {
			t.Fatal("the thumbnail job did not end")
		}
		time.Sleep(100 * time.Millisecond)
	}
	res := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/thumbnail", nil, owner.header(false))
	if res.status != http.StatusOK || !bytes.Equal(res.body, broken) {
		t.Errorf("the thumbnail: %d, %d bytes, want the file", res.status, len(res.body))
	}
	cove := createProject(t, s.main, owner, ws, "Cove").ID
	upload(t, s.main, owner, cove, broken, http.StatusCreated)
	if n := countRows(t, d, `SELECT count(*) FROM river_job WHERE kind = 'asset_thumbnail'`); n != 1 {
		t.Errorf("%d thumbnail jobs, want the first upload's alone", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM asset WHERE thumbnail_key = storage_key`); n != 2 {
		t.Errorf("%d assets are their own thumbnails, want both", n)
	}
	if files := storedFiles(t, dir); len(files) != 1 {
		t.Errorf("the store holds %v, want the file alone", files)
	}
}

// TestHoldSlot checks that the uploads take turns for their slots: one that
// finds every slot held waits until one is free before the operation reads
// its body, and one whose client goes away while it waits ends without
// reaching the operation.
func TestHoldSlot(t *testing.T) {
	t.Parallel()
	router := chi.NewMux()
	hapi := humachi.New(router, huma.DefaultConfig("Test", "1"))
	entered, release := make(chan string, 3), make(chan struct{})
	huma.Register(hapi, huma.Operation{Method: http.MethodPost, Path: "/{name}",
		Middlewares: huma.Middlewares{holdSlot(newSlots(1))}},
		func(_ context.Context, in *struct {
			Name    string `path:"name"`
			RawBody []byte
		}) (*struct{}, error) {
			entered <- in.Name
			<-release
			return nil, nil
		})
	post := func(ctx context.Context, name string) <-chan int {
		done := make(chan int, 1)
		go func() {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodPost, "/"+name,
				strings.NewReader("file")))
			done <- w.Code
		}()
		return done
	}
	first := post(context.Background(), "first")
	if name := <-entered; name != "first" {
		t.Fatalf("%s reached the operation first", name)
	}
	second := post(context.Background(), "second")
	ctx, cancel := context.WithCancel(context.Background())
	gone := post(ctx, "gone")
	select {
	case name := <-entered:
		t.Fatalf("%s reached the operation while the slot was held", name)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if code := <-gone; code != http.StatusInternalServerError {
		t.Errorf("a request whose client went away while it waited: %d", code)
	}
	close(release)
	if code := <-first; code != http.StatusNoContent {
		t.Errorf("the first request: %d", code)
	}
	select {
	case name := <-entered:
		if name != "second" {
			t.Errorf("%s reached the operation once the slot was free, want second", name)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second request did not reach the operation once the slot was free")
	}
	if code := <-second; code != http.StatusNoContent {
		t.Errorf("the second request: %d", code)
	}
	select {
	case name := <-entered:
		t.Errorf("%s reached the operation", name)
	default:
	}
}

// TestHoldSlotShare checks that a user holds no more than their share of
// the slots, a quarter: a request of a user who holds as many waits, while
// another user's takes a slot that is free, and once the user's request
// ends, the next of theirs takes its slot.
func TestHoldSlotShare(t *testing.T) {
	t.Parallel()
	router := chi.NewMux()
	// The request's user is the one that the header names, as the
	// authenticator would have found them.
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := ids.UUID{byte(len(r.Header.Get("User")))}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), currentKey{}, current{userID: user})))
		})
	})
	hapi := humachi.New(router, huma.DefaultConfig("Test", "1"))
	entered, release := make(chan string, 8), map[string]chan struct{}{}
	for _, name := range []string{"a1", "a2", "a3", "b1"} {
		release[name] = make(chan struct{})
	}
	slots := newSlots(8)
	huma.Register(hapi, huma.Operation{Method: http.MethodPost, Path: "/{name}",
		Middlewares: huma.Middlewares{holdSlot(slots)}},
		func(_ context.Context, in *struct {
			Name    string `path:"name"`
			RawBody []byte
		}) (*struct{}, error) {
			entered <- in.Name
			<-release[in.Name]
			return nil, nil
		})
	post := func(user, name string) <-chan int {
		done := make(chan int, 1)
		go func() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/"+name, strings.NewReader("file"))
			r.Header.Set("User", user)
			router.ServeHTTP(w, r)
			done <- w.Code
		}()
		return done
	}
	reached := func(want string) {
		t.Helper()
		select {
		case name := <-entered:
			if name != want {
				t.Fatalf("%s reached the operation, want %s", name, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not reach the operation", want)
		}
	}
	// A quarter of 8 slots is 2.
	a1, a2 := post("a", "a1"), post("a", "a2")
	got := map[string]bool{}
	for range 2 {
		got[<-entered] = true
	}
	if !got["a1"] || !got["a2"] {
		t.Fatalf("%v reached the operation, want a1 and a2", got)
	}
	a3 := post("a", "a3")
	select {
	case name := <-entered:
		t.Fatalf("%s reached the operation while its user held their share", name)
	case <-time.After(100 * time.Millisecond):
	}
	b1 := post("bb", "b1")
	reached("b1")
	close(release["a1"])
	reached("a3")
	for _, name := range []string{"a2", "a3", "b1"} {
		close(release[name])
	}
	for _, done := range []<-chan int{a1, a2, a3, b1} {
		if code := <-done; code != http.StatusNoContent {
			t.Errorf("a request: %d", code)
		}
	}
	slots.mu.Lock()
	defer slots.mu.Unlock()
	if len(slots.users) != 0 || len(slots.all) != 0 {
		t.Errorf("once every request has ended, %d users keep slots, and %d are held", len(slots.users),
			len(slots.all))
	}
}

// TestReconcileMendsThumbnails checks that the orphan reconciliation mends
// the thumbnails of the files that the store has: it enqueues the job of an
// asset that has none, as when its job ran out of attempts, and forgets a
// thumbnail that the store lost, and enqueues its job, which then make the
// thumbnail, while it enqueues no job of an asset whose job waits.
func TestReconcileMendsThumbnails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, d, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_ASSET_STORAGE": dir})
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	large := testPNG(t, 600, 300)
	a := upload(t, s.main, owner, p, large, http.StatusCreated)
	jobsOf := func(state string) int {
		return countRows(t, d, `SELECT count(*) FROM river_job WHERE kind = 'asset_thumbnail' AND state::text LIKE $1`,
			state)
	}
	reconcile := func() {
		t.Helper()
		if _, err := s.assets.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	if n := jobsOf("%"); n != 1 {
		t.Errorf("%d thumbnail jobs once the reconciliation ran while the upload's waits, want 1", n)
	}
	// The job ran out of attempts.
	admin := d.Admin(t)
	if _, err := admin.Exec(context.Background(),
		`UPDATE river_job SET state = 'discarded', finalized_at = now() WHERE kind = 'asset_thumbnail'`); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if n := jobsOf("available"); n != 1 {
		t.Errorf("%d thumbnail jobs wait once the reconciliation ran, want 1", n)
	}
	thumbnail := func() []byte {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); ; {
			res := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/thumbnail", nil, owner.header(false))
			if _, err := png.DecodeConfig(bytes.NewReader(res.body)); err == nil && !bytes.Equal(res.body, large) {
				return res.body
			}
			if time.Now().After(deadline) {
				t.Fatal("the thumbnail job did not make the thumbnail")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	startWorkers(t, s, d)
	made := thumbnail()
	var thumb string
	for _, f := range storedFiles(t, dir) {
		if strings.HasSuffix(f, ".thumb.png") {
			thumb = f
		}
	}
	if thumb == "" {
		t.Fatalf("the store holds %v, want a thumbnail", storedFiles(t, dir))
	}
	// The store lost the thumbnail, which the route then finds nothing of.
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(thumb))); err != nil {
		t.Fatal(err)
	}
	res := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/thumbnail", nil, owner.header(false))
	wantCode(t, "a thumbnail that the store lost", res, http.StatusNotFound, api.CodeNotFound)
	reconcile()
	if again := thumbnail(); !bytes.Equal(again, made) {
		t.Error("the thumbnail made again differs")
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(thumb))); err != nil {
		t.Errorf("the thumbnail made again: %v", err)
	}
}

// TestUploadAgainMendsItsFile checks that uploading a file that the project
// has already, whose blob the store lost, as when files were not all copied
// to a new store, stores the blob again, so that the asset is served again.
func TestUploadAgainMendsItsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, _, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_ASSET_STORAGE": dir})
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	data := testPNG(t, 9, 9)
	a := upload(t, s.main, owner, p, data, http.StatusCreated)
	files := storedFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("the store holds %v, want the file", files)
	}
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(files[0]))); err != nil {
		t.Fatal(err)
	}
	content := func() response {
		return call(s.main, http.MethodGet, assetPath(p, a.ID)+"/content", nil, owner.header(false))
	}
	wantCode(t, "a file that the store lost", content(), http.StatusNotFound, api.CodeNotFound)
	if again := upload(t, s.main, owner, p, data, http.StatusOK); again != a {
		t.Errorf("the same file again: %+v, want %+v", again, a)
	}
	if res := content(); res.status != http.StatusOK || !bytes.Equal(res.body, data) {
		t.Errorf("the file once it is uploaded again: %d, %d bytes", res.status, len(res.body))
	}
}

// TestUploadAgainMendsItsThumbnail checks that uploading a file that the
// project has already, whose thumbnail job ended without a thumbnail, as when
// the store had lost the file when the job ran, enqueues the job again, which
// then makes the thumbnail, while uploading it again while its job waits
// enqueues no other.
func TestUploadAgainMendsItsThumbnail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, d, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_ASSET_STORAGE": dir})
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	large := testPNG(t, 600, 300)
	a := upload(t, s.main, owner, p, large, http.StatusCreated)
	upload(t, s.main, owner, p, large, http.StatusOK)
	jobsOf := func(state string) int {
		return countRows(t, d, `SELECT count(*) FROM river_job WHERE kind = 'asset_thumbnail' AND state::text LIKE $1`,
			state)
	}
	if n := jobsOf("%"); n != 1 {
		t.Errorf("%d thumbnail jobs once the file is uploaded again while its job waits, want 1", n)
	}
	files := storedFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("the store holds %v, want the file", files)
	}
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(files[0]))); err != nil {
		t.Fatal(err)
	}
	startWorkers(t, s, d)
	for deadline := time.Now().Add(30 * time.Second); jobsOf("cancelled") != 1; {
		if time.Now().After(deadline) {
			t.Fatal("the thumbnail job of a file that the store lost did not end")
		}
		time.Sleep(100 * time.Millisecond)
	}
	upload(t, s.main, owner, p, large, http.StatusOK)
	for deadline := time.Now().Add(30 * time.Second); ; {
		res := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/thumbnail", nil, owner.header(false))
		if cfg, err := png.DecodeConfig(bytes.NewReader(res.body)); err == nil && !bytes.Equal(res.body, large) {
			if cfg.Width != asset.ThumbnailSide {
				t.Errorf("the thumbnail is %d pixels wide, want %d", cfg.Width, asset.ThumbnailSide)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("uploading the file again made no thumbnail")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestS3Assets checks the S3 driver end to end, against MinIO, which only
// configuration chooses: an upload stores the file in the bucket, and the
// routes that serve it answer with a redirect to a URL of the bucket that
// the server signed, with the file's type and disposition, once they have
// checked the access, and the URL reads the file; another workspace's user
// gets no URL.
//
// Verifies: FR-ASSET-01/AC1, FR-ASSET-01/AC2
func TestS3Assets(t *testing.T) {
	t.Parallel()
	bucket := blobtest.S3(t)
	storage := fmt.Sprintf("s3://%s:%s@%s:%d/%s?security=none", bucket.AccessKeyID,
		url.QueryEscape(bucket.SecretAccessKey.Reveal()), bucket.Host, bucket.Port, bucket.Bucket)
	s, _, owner, ws := inviteServer(t, "open", map[string]string{"STORYCREEP_ASSET_STORAGE": storage})
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	data := testPNG(t, 20, 10)
	a := upload(t, s.main, owner, p, data, http.StatusCreated)
	res := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/content", nil, owner.header(false))
	location := res.header.Get("Location")
	// The browser reuses the redirect a minute less than the URL works.
	if res.status != http.StatusFound || !strings.Contains(location, "/"+bucket.Bucket+"/") ||
		!strings.Contains(location, "X-Amz-Signature=") || res.header.Get("Cache-Control") != "private, max-age=240" {
		t.Fatalf("the file: %d, Location %q, Cache-Control %q", res.status, location,
			res.header.Get("Cache-Control"))
	}
	got, err := http.Get(location)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	body, _ := io.ReadAll(got.Body)
	if got.StatusCode != http.StatusOK || !bytes.Equal(body, data) || got.Header.Get("Content-Type") != "image/png" ||
		got.Header.Get("Content-Disposition") != `inline; filename="`+a.ID+`.png"` ||
		got.Header.Get("Cache-Control") != "private, max-age=300" {
		t.Errorf("the signed URL: %d, %d bytes, %v", got.StatusCode, len(body), got.Header)
	}
	// The bucket itself serves nothing without a signature.
	unsigned := strings.SplitN(location, "?", 2)[0]
	if res, err := http.Get(unsigned); err != nil {
		t.Fatal(err)
	} else if res.Body.Close(); res.StatusCode != http.StatusForbidden {
		t.Errorf("the object without a signature: %d, want 403", res.StatusCode)
	}

	jo := newNamedAccount(t, s.main, "jo@example.com", "Jo")
	wantCode(t, "another workspace's user", call(s.main, http.MethodGet, assetPath(p, a.ID)+"/content", nil,
		jo.header(false)), http.StatusNotFound, api.CodeNotFound)

	// Until its thumbnail is made, the thumbnail of a large image redirects
	// to the file's own route for half a minute, whose redirect to the bucket
	// the browser keeps, rather than to a URL of the bucket signed anew.
	large := upload(t, s.main, owner, p, testPNG(t, 600, 300), http.StatusCreated)
	pending := call(s.main, http.MethodGet, assetPath(p, large.ID)+"/thumbnail", nil, owner.header(false))
	if pending.status != http.StatusFound || pending.header.Get("Location") != assetPath(p, large.ID)+"/content" ||
		pending.header.Get("Cache-Control") != "private, max-age=30" {
		t.Errorf("the thumbnail before its job: %d, Location %q, Cache-Control %q; want a redirect to the file's "+
			"route, kept for 30 s", pending.status, pending.header.Get("Location"), pending.header.Get("Cache-Control"))
	}
}

// TestAssetsTokenScopes checks that an API token uploads with project:write
// and reads with project:read, which snapshot:read is not enough for.
func TestAssetsTokenScopes(t *testing.T) {
	t.Parallel()
	s, _, owner, ws := inviteServer(t, "open", nil)
	p := createProject(t, s.main, owner, ws, "Lighthouse").ID
	a := upload(t, s.main, owner, p, testPNG(t, 4, 4), http.StatusCreated)
	write := makeToken(t, s.main, owner, workspaceTokensPath(ws), "project:write")
	read := makeToken(t, s.main, owner, workspaceTokensPath(ws), "project:read")
	releases := makeToken(t, s.main, owner, workspaceTokensPath(ws), "snapshot:read")
	if res := send(s.main, http.MethodPost, assetPath(p, ""), testPNG(t, 5, 5), "image/png",
		bearer(write.Secret)); res.status != http.StatusCreated {
		t.Errorf("upload with project:write: %d %s", res.status, res.body)
	}
	wantCode(t, "upload with project:read", send(s.main, http.MethodPost, assetPath(p, ""), testPNG(t, 6, 6),
		"image/png", bearer(read.Secret)), http.StatusForbidden, api.CodeInsufficientScope)
	if res := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/content", nil, bearer(read.Secret)); res.status !=
		http.StatusOK {
		t.Errorf("read with project:read: %d", res.status)
	}
	wantCode(t, "read with snapshot:read", call(s.main, http.MethodGet, assetPath(p, a.ID), nil,
		bearer(releases.Secret)), http.StatusForbidden, api.CodeInsufficientScope)
}

// TestSetupChoosesBucket checks the storage step of first-run setup: without
// STORYCREEP_ASSET_STORAGE, the files of the assets go to the bucket that
// setup chose, which then serves them through URLs that the server signs.
func TestSetupChoosesBucket(t *testing.T) {
	t.Parallel()
	bucket := blobtest.S3(t)
	s, _ := pendingServer(t, map[string]string{"STORYCREEP_ASSET_STORAGE": ""})
	if st, err := s.setups.Status(context.Background()); err != nil || st.StorageFixed {
		t.Fatalf("the setup's status: %+v, %v; want the storage not fixed", st, err)
	}
	setup := completeSetup(t, s, "admin@example.com", map[string]any{"storage": map[string]any{
		"host": bucket.Host, "port": bucket.Port, "security": "none", "bucket": bucket.Bucket,
		"access_key_id": bucket.AccessKeyID, "secret_access_key": bucket.SecretAccessKey.Reveal()}})
	owner := signIn(t, s.main, "admin@example.com")
	p := createProject(t, s.main, owner, setup.Workspace.ID, "Lighthouse").ID
	data := testPNG(t, 12, 12)
	a := upload(t, s.main, owner, p, data, http.StatusCreated)
	res := call(s.main, http.MethodGet, assetPath(p, a.ID)+"/content", nil, owner.header(false))
	if res.status != http.StatusFound || !strings.Contains(res.header.Get("Location"), "/"+bucket.Bucket+"/") {
		t.Fatalf("the file: %d, Location %q", res.status, res.header.Get("Location"))
	}
	got, err := http.Get(res.header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	if body, _ := io.ReadAll(got.Body); got.StatusCode != http.StatusOK || !bytes.Equal(body, data) {
		t.Errorf("the signed URL: %d, %d bytes", got.StatusCode, len(body))
	}
}
