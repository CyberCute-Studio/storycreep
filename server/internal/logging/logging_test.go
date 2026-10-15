package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"testing/slogtest"
)

// records decodes the JSON lines in buf.
func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(buf.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not a JSON line: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func requestContext() context.Context {
	ctx := WithRequestID(context.Background(), "req-1")
	ctx = WithWorkspaceID(ctx, "ws_1")
	return WithUserID(ctx, "usr_1")
}

// TestContextFields checks that records carry the request, workspace, and
// user IDs of their context as top-level JSON fields.
//
// Verifies: NFR-OBS-01
func TestContextFields(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo)
	ctx := requestContext()

	log.InfoContext(ctx, "plain", "k", "v")
	log.With("component", "db").InfoContext(ctx, "with attrs")
	log.WithGroup("db").InfoContext(ctx, "in group", "query", "q")
	log.WithGroup("db").With("pool", "main").WithGroup("tx").InfoContext(ctx, "nested", "rows", 3)
	log.Info("no context")
	log.InfoContext(WithUserID(ctx, ""), "user cleared")

	got := records(t, &buf)
	if len(got) != 6 {
		t.Fatalf("got %d records, want 6", len(got))
	}
	for _, r := range got[:4] {
		for k, want := range map[string]string{KeyRequestID: "req-1", KeyWorkspaceID: "ws_1", KeyUserID: "usr_1"} {
			if r[k] != want {
				t.Errorf("record %q: %s = %v, want %q", r["msg"], k, r[k], want)
			}
		}
	}
	if got[0]["k"] != "v" || got[1]["component"] != "db" {
		t.Errorf("record attributes lost: %v, %v", got[0], got[1])
	}
	if db, _ := got[2]["db"].(map[string]any); db["query"] != "q" || db[KeyRequestID] != nil {
		t.Errorf("group attributes = %v, want query inside the group and no request ID there", got[2]["db"])
	}
	db, _ := got[3]["db"].(map[string]any)
	tx, _ := db["tx"].(map[string]any)
	if db["pool"] != "main" || tx["rows"] != float64(3) {
		t.Errorf("nested groups = %v, want db.pool and db.tx.rows", got[3]["db"])
	}
	for _, k := range []string{KeyRequestID, KeyWorkspaceID, KeyUserID} {
		if _, ok := got[4][k]; ok {
			t.Errorf("a record without context fields has %s", k)
		}
	}
	if _, ok := got[5][KeyUserID]; ok || got[5][KeyRequestID] != "req-1" {
		t.Errorf("clearing the user ID: got %v", got[5])
	}
}

func TestLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelWarn)
	log.Info("dropped")
	log.Warn("kept")
	got := records(t, &buf)
	if len(got) != 1 || got[0]["msg"] != "kept" || got[0]["level"] != "WARN" {
		t.Errorf("got %v, want only the warning", got)
	}
}

// TestHandlerContract runs the standard library's conformance test for
// slog handlers.
func TestHandlerContract(t *testing.T) {
	var buf bytes.Buffer
	err := slogtest.TestHandler(New(&buf, slog.LevelDebug).Handler(), func() []map[string]any {
		return records(t, &buf)
	})
	if err != nil {
		t.Error(err)
	}
}
