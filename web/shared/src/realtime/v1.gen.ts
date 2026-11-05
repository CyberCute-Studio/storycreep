// Generated from schema/realtime/v1/messages.schema.json. Do not edit; run
// "go test ./internal/realtime -run TestSchemaFile -update" in server/ instead.

/** A message that a client sends. */
export type ClientMessage = SubscribeMessage | UnsubscribeMessage;

export interface ErrorMessage {
  /** invalid_message: the message is not a JSON object of one of the client's types, with their fields */
  code: "invalid_message";
  /** error */
  type: "error";
}

export interface HeartbeatMessage {
  /** heartbeat */
  type: "heartbeat";
}

export interface OperationCommand {
  /** The command's ID, which its client gave it */
  command_id: string;
  /** The command's payload, as the command endpoint's document describes it for the type */
  payload: unknown;
  /** The command's type, as node.create */
  type: string;
}

export interface OperationMessage {
  /** The batch's commands, in the order that they applied */
  commands: Array<OperationCommand> | null;
  /** When the batch applied */
  created_at: string;
  /** The project's ID, as prj_… */
  project_id: string;
  /** The entities that the batch changed, each with its revision after the batch */
  revisions: Array<OperationRevision> | null;
  /** The batch's seq, the next of the project's */
  seq: number;
  /** operation */
  type: "operation";
}

export interface OperationRevision {
  /** The entity's ID, as nd_… */
  id: string;
  /** The entity's revision after the batch */
  rev: number;
}

export interface ResyncRequiredMessage {
  /** The project's ID, as prj_… */
  project_id: string;
  /** The seq that the subscription goes on from: the operations after it follow */
  seq: number;
  /** resync_required */
  type: "resync_required";
}

/** A message that the server sends. */
export type ServerMessage = SubscribedMessage | ResyncRequiredMessage | OperationMessage | UnsubscribedMessage | SubscriptionFailedMessage | ErrorMessage | HeartbeatMessage;

export interface SubscribeMessage {
  /** The project's ID, as prj_… */
  project_id: string;
  /** The seq of the project's latest operation that the client has, 0 for none: the operations after it are sent first. Without it, only those that commit from now on are. */
  since?: number;
  /** subscribe */
  type: "subscribe";
}

export interface SubscribedMessage {
  /** The project's ID, as prj_… */
  project_id: string;
  /** The seq of the project's latest operation when the subscription started: those after since, up to it, follow first */
  seq: number;
  /** subscribed */
  type: "subscribed";
}

export interface SubscriptionFailedMessage {
  /** invalid_id, the ID is not a project's; not_found, there is no such project, or the user cannot read it, which the answer does not tell apart; rate_limited, the user has subscribed too often, and retries after retry_after seconds; or too_many_subscriptions, the socket subscribes to 100 projects already */
  code: "invalid_id" | "not_found" | "rate_limited" | "too_many_subscriptions";
  /** The project's ID, as prj_…, or, for invalid_id, as the subscription named it */
  project_id: string;
  /** For rate_limited, how many seconds to wait before subscribing again */
  retry_after?: number;
  /** subscription_failed */
  type: "subscription_failed";
}

export interface UnsubscribeMessage {
  /** The project's ID, as prj_… */
  project_id: string;
  /** unsubscribe */
  type: "unsubscribe";
}

export interface UnsubscribedMessage {
  /** The project's ID, as prj_…, or, for an unsubscription of an ID that is not a project's, as it named it */
  project_id: string;
  /** requested, as the client asked, or not_found, the project can no longer be read */
  reason: "requested" | "not_found";
  /** unsubscribed */
  type: "unsubscribed";
}
