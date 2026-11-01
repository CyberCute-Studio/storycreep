# Real-time messages

The real-time channel is the WebSocket of `GET /api/v1/ws`, over which a client subscribes to the projects that it reads and is sent each batch of commands that commits to them, from every server of the instance, in the order of their seqs (PRD FR-RT-01, FR-RT-02, plan note N59). The operation log is the source of truth, and the socket only a way to be told of it sooner: a client that connects again subscribes with the seq of the latest operation that it has, and is sent those that it missed.

| File | Contents |
| --- | --- |
| [`v1/messages.schema.json`](v1/messages.schema.json) | The JSON Schema of the messages: a `ClientMessage`, which a client sends, or a `ServerMessage`, which the server sends, each one of its types. Its `$id` is the URL of the file on the main branch. `server/internal/realtime` generates it from its types, and `go test ./...` fails when it is out of date; `go test ./internal/realtime -run TestSchemaFile -update`, in `server/`, rewrites it. |

## Messages

Each message is a JSON object in a text message of its own, whose `type` names it.

A client sends:

- `subscribe`, with `project_id` and, optionally, `since`, the seq of the project's latest operation that it has, `0` for none.
- `unsubscribe`, with `project_id`.

A client's message holds exactly its type's fields, with each that the schema requires, at most 4 KiB, or the server answers it with `error` (`invalid_message`), or closes the socket with 1009 when it is larger. A `subscribe` to a project that the socket subscribes to already starts the subscription anew once it holds; one that is refused leaves the subscription as it was, but for `not_found`, which ends it.

The server sends:

- `subscribed`, with the project's `seq`: the operations after `since`, up to `seq`, follow, and then each that commits. Without `since`, only those that commit from now on do.
- `resync_required`, with the `seq` that the subscription goes on from: the operations after `since` cannot be sent, because the log no longer has one of them, as once compaction has dropped it, more than 10,000 follow `since`, or `since` is beyond the project's seq. The client reloads the project, and passes over an operation that follows whose revisions it has already. It may come during a subscription too, when the log turns out not to have an operation that the subscription was not sent, more than 10,000 wait to be sent at once, as once the server has missed the notifications of many, or the project's seq has gone back behind what the subscription was sent, as once the database is restored from a backup older than it, whose operations after its seq, as they commit anew, are not those that the client has.
- `operation`, a batch that committed: its `seq`, the next of the project's, its `commands`, each with its `command_id`, its `type`, and its `payload`, as the command endpoint's document describes them, the `revisions` of the entities that it changed, and `created_at`.
- `subscription_failed`, with a `code`: `invalid_id`; `not_found`, for a project that does not exist and one that the user cannot read alike; `rate_limited`, with `retry_after` in seconds, since each subscription counts in the rate limit of reads; or `too_many_subscriptions`, past 100 of one socket.
- `unsubscribed`, with a `reason`: `requested`, as the client asked, whether the socket subscribed to the project or not, or `not_found`, once a check finds that the project can no longer be read, as once the user is removed from its workspace, or it goes to the trash, which is at the project's next operation.
- `error`, for a message that the server cannot read.
- `heartbeat`, every 25 seconds, beside a ping, whose pong a browser sends by itself, between the operations that the server sends too. The server closes the socket with 1008 once it has heard nothing from the client for 75 seconds, neither a message nor a pong, however many operations it has written to it meanwhile, since the connection's buffers take those of a client that has gone without a word, as a laptop that sleeps, and drops the connection without a close, which the client sees as 1006, once a message to it takes longer than 30 seconds to write, since the close would wait behind it. The pong of a slow client comes after the messages that it is still reading, so the 75 seconds leave it the 30 that a message may take. A client that hears nothing for a minute takes the connection for lost.

The server's messages name a project by its ID as the API writes it, whichever spelling of it the client's message had, but for `subscription_failed` with `invalid_id`, and `unsubscribed` for an ID that is not a project's, which name it as the client's message did.

A server's message may hold properties that its schema does not name, which a later server adds and a client ignores. A change that a client could not ignore is a version of its own, with a schema at a path of its version.

## Closing

The socket's session or API token, and the user's access to each project, are checked again before each subscription and each page of operations that it is sent, and not while it is sent nothing. The server closes the socket with:

- 4001 once a check finds that the credential that the socket was opened with no longer holds: its session has ended, or has a new secret, as a change of the account's password gives the session that made it, with the cookie that the change sets, or its token no longer holds. The client makes a request of the API with the credential that it holds now, as `GET /api/v1/auth/session` with the cookie, since a browser's socket does not tell the status that refused its upgrade, and connects again if the request is answered, or signs in again, or takes another token, if it is refused with 401.
- 1001 as the server stops, 1008 once the server has heard nothing from the client for 75 seconds, and 1011 when the server fails, as when it cannot read the database, or cannot answer the client's ping in time, behind a message that it still writes: the client connects again, with exponential backoff and jitter, and subscribes with the seqs that it has, as it does once the connection drops without a close (1006), as when a message to it takes longer than 30 seconds to write.
