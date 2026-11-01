package realtime

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/CyberCute-Studio/storycreep/server/internal/commands"
	"github.com/CyberCute-Studio/storycreep/server/internal/ids"
)

var update = flag.Bool("update", false, "rewrite the real-time messages' JSON Schema from their Go types")

// schemaFile is the messages' JSON Schema in the repository.
var schemaFile = filepath.Join("..", "..", "..", filepath.FromSlash(SchemaPath))

// TestSchemaFile checks that the real-time messages' JSON Schema in the
// repository is the one that Schema generates from their types, so that the
// documented messages cannot drift from those that the hub reads and writes,
// and that its $id names its version. Run go test ./internal/realtime -run
// TestSchemaFile -update in server/ to write it again.
func TestSchemaFile(t *testing.T) {
	want := Schema()
	if *update {
		if err := os.MkdirAll(filepath.Dir(schemaFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(schemaFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is not what Schema generates; run go test ./internal/realtime -run TestSchemaFile -update",
			SchemaPath)
	}
	if !strings.Contains(SchemaID, "/v1/") || !strings.HasSuffix(SchemaID, SchemaPath) {
		t.Errorf("the schema's $id %s does not name the version at %s", SchemaID, SchemaPath)
	}
}

// TestMessagesMatchSchema checks that a message of each type that the server
// sends, as the hub writes it, is a ServerMessage of the schema, and only
// that, that the messages that a client sends are ClientMessages, and that
// the schema refuses what the hub refuses of a client.
func TestMessagesMatchSchema(t *testing.T) {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Schema()))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource(SchemaID, doc); err != nil {
		t.Fatal(err)
	}
	compile := func(def string) *jsonschema.Schema {
		s, err := c.Compile(SchemaID + "#/$defs/" + def)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	client, server := compile("ClientMessage"), compile("ServerMessage")
	project := ids.Encode(ids.Project, ids.New())
	since := int64(3)
	op := commands.Operation{Seq: 4, CreatedAt: time.Now(), Commands: []commands.LoggedCommand{{ID: ids.New(),
		Type: "node.create", Payload: json.RawMessage(`{"id": "nd_1"}`)}},
		Revisions: []commands.Revision{{Ref: commands.Ref{Kind: ids.Node, ID: ids.New()}, Rev: 1}}}
	sent := []any{
		SubscribedMessage{Type: typeSubscribed, ProjectID: project, Seq: 3},
		ResyncRequiredMessage{Type: typeResyncRequired, ProjectID: project, Seq: 0},
		operationMessage(project, op),
		operationMessage(project, commands.Operation{Seq: 1, Commands: op.Commands, Revisions: []commands.Revision{}}),
		UnsubscribedMessage{Type: typeUnsubscribed, ProjectID: project, Reason: "not_found"},
		SubscriptionFailedMessage{Type: typeSubscriptionFailed, ProjectID: "prj_x", Code: "invalid_id"},
		SubscriptionFailedMessage{Type: typeSubscriptionFailed, ProjectID: project, Code: "rate_limited",
			RetryAfter: 12},
		invalidMessage,
		HeartbeatMessage{Type: typeHeartbeat},
	}
	for _, msg := range sent {
		v := instance(t, msg)
		if err := server.Validate(v); err != nil {
			t.Errorf("%T is not a ServerMessage: %v", msg, err)
		}
		if client.Validate(v) == nil {
			t.Errorf("%T is a ClientMessage too", msg)
		}
	}
	for _, msg := range []any{
		SubscribeMessage{Type: typeSubscribe, ProjectID: project, Since: &since},
		SubscribeMessage{Type: typeSubscribe, ProjectID: project},
		UnsubscribeMessage{Type: typeUnsubscribe, ProjectID: project},
	} {
		if err := client.Validate(instance(t, msg)); err != nil {
			t.Errorf("%T is not a ClientMessage: %v", msg, err)
		}
	}
	for _, raw := range []string{
		`{"type": "subscribe", "project_id": "prj_x", "since": -1}`,
		`{"type": "subscribe", "project_id": "prj_x", "seq": 1}`,
		`{"type": "subscribe"}`,
		`{"type": "ping"}`,
	} {
		v, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if client.Validate(v) == nil {
			t.Errorf("the schema takes %s, which the hub refuses", raw)
		}
	}
}

// instance returns msg as the validator reads JSON.
func instance(t *testing.T, msg any) any {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
