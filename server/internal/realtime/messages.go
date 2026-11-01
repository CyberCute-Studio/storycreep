package realtime

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// The messages of the real-time channel (PRD FR-RT-01, plan note N59), each
// a JSON object in a text message of its own, whose type its type field
// names. A client subscribes to a project and unsubscribes from it; the
// server answers each subscription, sends the operations of the projects
// subscribed to, and a heartbeat every 25 seconds, so that a client that
// hears nothing for a minute knows that the connection is gone, and connects
// again. Schema describes them all, and a client ignores a field that it
// does not know, which a later server may add: the schema of a server's
// message takes properties that it does not name, and a client's does not,
// since the server refuses them.

// The types of the messages.
const (
	typeSubscribe          = "subscribe"
	typeUnsubscribe        = "unsubscribe"
	typeSubscribed         = "subscribed"
	typeResyncRequired     = "resync_required"
	typeOperation          = "operation"
	typeUnsubscribed       = "unsubscribed"
	typeSubscriptionFailed = "subscription_failed"
	typeError              = "error"
	typeHeartbeat          = "heartbeat"
)

// SubscribeMessage subscribes the socket to a project.
type SubscribeMessage struct {
	Type      string `json:"type" enum:"subscribe" doc:"subscribe"`
	ProjectID string `json:"project_id" doc:"The project's ID, as prj_…"`
	Since     *int64 `json:"since,omitempty" minimum:"0" doc:"The seq of the project's latest operation that the client has, 0 for none: the operations after it are sent first. Without it, only those that commit from now on are."`
}

// UnsubscribeMessage ends the socket's subscription to a project.
type UnsubscribeMessage struct {
	Type      string `json:"type" enum:"unsubscribe" doc:"unsubscribe"`
	ProjectID string `json:"project_id" doc:"The project's ID, as prj_…"`
}

// SubscribedMessage answers a subscription that holds: the operations after
// its since, up to seq, follow, and then each that commits.
type SubscribedMessage struct {
	_         struct{} `additionalProperties:"true"`
	Type      string   `json:"type" enum:"subscribed" doc:"subscribed"`
	ProjectID string   `json:"project_id" doc:"The project's ID, as prj_…"`
	Seq       int64    `json:"seq" minimum:"0" doc:"The seq of the project's latest operation when the subscription started: those after since, up to it, follow first"`
}

// ResyncRequiredMessage tells a subscriber that the operations that it
// missed cannot be sent: it reloads the project, and is sent the operations
// after seq, which a project reloaded later may have already, by their
// revisions.
type ResyncRequiredMessage struct {
	_         struct{} `additionalProperties:"true"`
	Type      string   `json:"type" enum:"resync_required" doc:"resync_required"`
	ProjectID string   `json:"project_id" doc:"The project's ID, as prj_…"`
	Seq       int64    `json:"seq" minimum:"0" doc:"The seq that the subscription goes on from: the operations after it follow"`
}

// OperationMessage is a batch of commands that committed to a project that
// the socket subscribes to, its seq one more than the last that the
// subscription was sent.
type OperationMessage struct {
	_         struct{}            `additionalProperties:"true"`
	Type      string              `json:"type" enum:"operation" doc:"operation"`
	ProjectID string              `json:"project_id" doc:"The project's ID, as prj_…"`
	Seq       int64               `json:"seq" minimum:"1" doc:"The batch's seq, the next of the project's"`
	Commands  []OperationCommand  `json:"commands" minItems:"1" doc:"The batch's commands, in the order that they applied"`
	Revisions []OperationRevision `json:"revisions" doc:"The entities that the batch changed, each with its revision after the batch"`
	CreatedAt time.Time           `json:"created_at" doc:"When the batch applied"`
}

// OperationCommand is a command of an operation, as its client sent it.
type OperationCommand struct {
	_         struct{}        `additionalProperties:"true"`
	CommandID string          `json:"command_id" format:"uuid" doc:"The command's ID, which its client gave it"`
	Type      string          `json:"type" doc:"The command's type, as node.create"`
	Payload   json.RawMessage `json:"payload" doc:"The command's payload, as the command endpoint's document describes it for the type"`
}

// OperationRevision is an entity that an operation changed.
type OperationRevision struct {
	_   struct{} `additionalProperties:"true"`
	ID  string   `json:"id" doc:"The entity's ID, as nd_…"`
	Rev int64    `json:"rev" doc:"The entity's revision after the batch"`
}

// UnsubscribedMessage ends a subscription: as the client asked, or because
// the project can no longer be read, as once the user's access to it is
// removed, or it is deleted.
type UnsubscribedMessage struct {
	_         struct{} `additionalProperties:"true"`
	Type      string   `json:"type" enum:"unsubscribed" doc:"unsubscribed"`
	ProjectID string   `json:"project_id" doc:"The project's ID, as prj_…, or, for an unsubscription of an ID that is not a project's, as it named it"`
	Reason    string   `json:"reason" enum:"requested,not_found" doc:"requested, as the client asked, or not_found, the project can no longer be read"`
}

// SubscriptionFailedMessage refuses a subscription.
type SubscriptionFailedMessage struct {
	_         struct{} `additionalProperties:"true"`
	Type      string   `json:"type" enum:"subscription_failed" doc:"subscription_failed"`
	ProjectID string   `json:"project_id" doc:"The project's ID, as prj_…, or, for invalid_id, as the subscription named it"`
	Code      string   `json:"code" enum:"invalid_id,not_found,rate_limited,too_many_subscriptions" doc:"invalid_id, the ID is not a project's; not_found, there is no such project, or the user cannot read it, which the answer does not tell apart; rate_limited, the user has subscribed too often, and retries after retry_after seconds; or too_many_subscriptions, the socket subscribes to 100 projects already"`
	// RetryAfter is how many seconds to wait, for rate_limited.
	RetryAfter int `json:"retry_after,omitempty" minimum:"1" doc:"For rate_limited, how many seconds to wait before subscribing again"`
}

// ErrorMessage answers a message that the server cannot read.
type ErrorMessage struct {
	_    struct{} `additionalProperties:"true"`
	Type string   `json:"type" enum:"error" doc:"error"`
	Code string   `json:"code" enum:"invalid_message" doc:"invalid_message: the message is not a JSON object of one of the client's types, with their fields"`
}

// HeartbeatMessage is sent every 25 seconds.
type HeartbeatMessage struct {
	_    struct{} `additionalProperties:"true"`
	Type string   `json:"type" enum:"heartbeat" doc:"heartbeat"`
}

// SchemaID is the $id of the messages' JSON Schema, which names its version.
const SchemaID = "https://raw.githubusercontent.com/CyberCute-Studio/storycreep/main/" + SchemaPath

// SchemaPath is where the repository holds the messages' JSON Schema, from
// its root.
const SchemaPath = "schema/realtime/v1/messages.schema.json"

// clientMessages and serverMessages are the types of the messages that a
// client sends and that the server sends.
var (
	clientMessages = []reflect.Type{reflect.TypeFor[SubscribeMessage](), reflect.TypeFor[UnsubscribeMessage]()}
	serverMessages = []reflect.Type{reflect.TypeFor[SubscribedMessage](), reflect.TypeFor[ResyncRequiredMessage](),
		reflect.TypeFor[OperationMessage](), reflect.TypeFor[UnsubscribedMessage](),
		reflect.TypeFor[SubscriptionFailedMessage](), reflect.TypeFor[ErrorMessage](),
		reflect.TypeFor[HeartbeatMessage]()}
)

// clientSchemas holds the schemas of the client's messages, as Schema writes
// them, and subscribeSchema and unsubscribeSchema are those of its types,
// which the socket holds a client's message to (validMessage).
var (
	clientSchemas     = huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	subscribeSchema   = clientSchemas.Schema(reflect.TypeFor[SubscribeMessage](), true, "")
	unsubscribeSchema = clientSchemas.Schema(reflect.TypeFor[UnsubscribeMessage](), true, "")
)

// validMessage reports whether data, a client's message, is what the schema
// s describes, as huma's validation holds the API's bodies to their schemas:
// a JSON object of its type's fields alone, with each that it requires, not
// null, each of its JSON type and within its bounds, so that the socket
// answers a message that the published schema refuses, as one without its
// project_id, with invalid_message, rather than take the field that
// encoding/json leaves empty for one that it misses. As in the API's bodies,
// null is taken for a field that may be left out, as since, left out.
func validMessage(s *huma.Schema, data []byte) bool {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	res := &huma.ValidateResult{}
	huma.Validate(clientSchemas, s, huma.NewPathBuffer([]byte{}, 0), huma.ModeWriteToServer, v, res)
	return len(res.Errors) == 0
}

// Schema returns the JSON Schema (2020-12) of the real-time channel's
// messages, as SchemaPath holds it: a message is a ClientMessage, which a
// client sends, or a ServerMessage, which the server sends, each one of its
// types, under $defs. A change of what a message holds, but for a field that
// it adds, which clients ignore, is a version of its own, at a path of its
// version, and a socket's URL of its own.
func Schema() []byte {
	r := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	union := func(types []reflect.Type) *huma.Schema {
		s := &huma.Schema{}
		for _, t := range types {
			s.OneOf = append(s.OneOf, r.Schema(t, true, ""))
		}
		return s
	}
	client, server := union(clientMessages), union(serverMessages)
	client.Description, server.Description = "A message that a client sends.", "A message that the server sends."
	defs := r.Map()
	defs["ClientMessage"], defs["ServerMessage"] = client, server
	doc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     SchemaID,
		"title":   "Storycreep real-time messages, version 1",
		"description": "The messages of the WebSocket of GET /api/v1/ws (PRD FR-RT-01): each a JSON object in a " +
			"text message of its own, whose type its type field names. A client subscribes to the projects that it " +
			"reads, and is sent the operations of each that it missed, and then each that commits, in the order " +
			"of their seqs.",
		"oneOf": []map[string]string{{"$ref": "#/$defs/ClientMessage"}, {"$ref": "#/$defs/ServerMessage"}},
		"$defs": defs,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		panic("realtime: the schema is not JSON: " + err.Error())
	}
	return append(data, '\n')
}
