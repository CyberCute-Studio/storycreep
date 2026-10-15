package apicontract

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/storycreep/server/internal/gitexec/gitexectest"
)

// copyFixture writes testdata/name to path.
func copyFixture(t *testing.T, name, path string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheck runs the check against a git repository whose branch removes a
// property from a response, as CI runs it against a pull request: the
// check reports the change, and a commit of the branch may allow it by
// saying why in an API-Break trailer.
//
// Verifies: FR-API-01/AC3
func TestCheck(t *testing.T) {
	repo, git := gitexectest.Repo(t)
	doc := filepath.Join(repo, "schema", "api", "v1", "openapi.json")
	copyFixture(t, "base.json", doc)
	git("add", ".")
	git("commit", "-q", "-m", "The first document")

	// The branch forks, and main moves on with an operation of its own,
	// which the branch does not have yet.
	git("switch", "-q", "-c", "feature")
	git("switch", "-q", "main")
	b, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	more := strings.Replace(string(b), `"paths": {`, `"paths": {
    "/more": {"get": {"operationId": "getMore", "responses": {"204": {"description": "No Content"}}}},`, 1)
	if err := os.WriteFile(doc, []byte(more), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-q", "-am", "Add an operation")
	git("switch", "-q", "feature")

	ctx := context.Background()
	res, err := Check(ctx, doc, "main")
	if err != nil || res.Missing || len(res.Changes) != 0 {
		t.Fatalf("a branch that only lacks main's newer operation: %+v, %v; want no change", res, err)
	}

	// Removing a property from a response breaks the API, committed or not.
	copyFixture(t, "response-field-removed.json", doc)
	want := inThings("name was removed")
	res, err = Check(ctx, doc, "main")
	if err != nil || !slices.Equal(messages(res.Changes), want) || len(res.Overrides) != 0 {
		t.Fatalf("a removed property: %+v, %v; want %q and no override", res, err, want)
	}
	git("commit", "-q", "-am", "Remove the name\n\nIt is not needed.")
	res, err = Check(ctx, doc, "main")
	if err != nil || !slices.Equal(messages(res.Changes), want) || len(res.Overrides) != 0 {
		t.Fatalf("a committed removal: %+v, %v; want %q and no override", res, err, want)
	}

	// An empty trailer, or one in the middle of the message, gives no reason.
	git("commit", "-q", "--allow-empty", "-m", "Explain\n\nAPI-Break: the reason\n\nis not a trailer here.")
	git("commit", "-q", "--allow-empty", "-m", "Explain\n\nAPI-Break:")
	if res, err = Check(ctx, doc, "main"); err != nil || len(res.Overrides) != 0 {
		t.Fatalf("no reason: %+v, %v; want no override", res, err)
	}

	// A commit may give more than one reason, each in a trailer of its own.
	git("commit", "-q", "--allow-empty", "-m", "Allow it\n\napi-break: no client depends on v1 before the first "+
		"release\nCo-Authored-By: A <a@example.com>\nAPI-Break: the fixture says so")
	head := strings.TrimSpace(git("rev-parse", "HEAD"))
	res, err = Check(ctx, doc, "main")
	if err != nil {
		t.Fatal(err)
	}
	wantOverrides := []Override{
		{Commit: head, Reason: "no client depends on v1 before the first release"},
		{Commit: head, Reason: "the fixture says so"},
	}
	if !slices.Equal(messages(res.Changes), want) || !slices.Equal(res.Overrides, wantOverrides) {
		t.Errorf("with API-Break trailers: %+v; want the change, and the trailers' reasons", res)
	}
}

func TestCheckBeforeTheFirstDocument(t *testing.T) {
	repo, git := gitexectest.Repo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "No document yet")
	git("switch", "-q", "-c", "feature")
	doc := filepath.Join(repo, "schema", "api", "v1", "openapi.json")
	copyFixture(t, "base.json", doc)

	res, err := Check(context.Background(), doc, "main")
	if err != nil || !res.Missing || len(res.Changes) != 0 {
		t.Errorf("Check = %+v, %v; want nothing to compare with", res, err)
	}
	if _, err := Check(context.Background(), doc, "no-such-branch"); err == nil {
		t.Errorf("Check against a missing branch succeeded")
	}
	if err := os.Remove(doc); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(context.Background(), doc, "main"); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Errorf("without the document: %v, want an error saying to restore it", err)
	}
}

// TestCheckWithSignatureChecks checks that the commit an override names is
// the commit, when the user's log.showSignature makes git log check each
// commit's signature and say what it found.
func TestCheckWithSignatureChecks(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	repo, git := gitexectest.Repo(t)
	key := filepath.Join(t.TempDir(), "key")
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen cannot make a key: %v\n%s", err, out)
	}
	doc := filepath.Join(repo, "schema", "api", "v1", "openapi.json")
	copyFixture(t, "base.json", doc)
	git("add", ".")
	git("commit", "-q", "-m", "The first document")
	git("switch", "-q", "-c", "feature")
	copyFixture(t, "response-field-removed.json", doc)
	git("config", "--global", "log.showSignature", "true")
	sign := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com",
		"-c", "gpg.format=ssh", "-c", "gpg.ssh.program="+keygen, "-c", "user.signingKey="+key,
		"commit", "-q", "-S", "-am", "Remove the name\n\nAPI-Break: no client depends on v1 before the first release")
	if out, err := sign.CombinedOutput(); err != nil {
		t.Skipf("git cannot sign a commit with ssh-keygen: %v\n%s", err, out)
	}
	head := strings.TrimSpace(git("rev-parse", "HEAD"))
	res, err := Check(context.Background(), doc, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Overrides) != 1 || res.Overrides[0].Commit != head {
		t.Errorf("Overrides = %+v, want one by %s", res.Overrides, head)
	}
}
