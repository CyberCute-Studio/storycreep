// The shared layer that applies commands to a project's document (PRD §10.6,
// FR-OFF-03 AC2): one applier for each type of command that the server
// registers, which changes a Draft as the command changes the project on the
// server, so that the page shows a batch's changes before the server has
// answered, and applies the operations of other sessions as they come over
// the real-time channel, and, offline, its queue's (plan item 91), with the
// same code.
//
// An applier changes what it can know from the command's payload and what
// the document holds. Where the server picks a value that the document
// cannot know, as a key of order among siblings that it does not hold, or a
// command changes entities that the document cannot tell, as the
// expressions that a rename of a variable rewrites, the applier says that
// its change is not sure, or makes none: the operation's revisions name
// every entity that the batch changed, and the store reads from the server
// each that the appliers did not surely change (reconcile, in store.ts).
// So an applier is a prediction that the server's answer confirms, never
// the source of truth.

import type { CommandBody } from "@storycreep/shared/api";
import { canonicalAnyId } from "@storycreep/shared/ids";
import { boardAppliers } from "./appliers/board";
import { componentAppliers } from "./appliers/component";
import { connectionAppliers } from "./appliers/connection";
import { nodeAppliers } from "./appliers/node";
import { otherAppliers } from "./appliers/other";
import { variableAppliers } from "./appliers/variable";
import type { Draft } from "./doc";

/** A type of command, as the server registers it. */
export type CommandType = CommandBody["type"];

/** The payload of a command of type T. */
export type PayloadOf<T extends CommandType> = Extract<
  CommandBody,
  { type: T }
>["payload"];

/** Changes draft as a command of type T with payload changes the project. */
export type Applier<T extends CommandType> = (
  draft: Draft,
  payload: PayloadOf<T>,
) => void;

/** An applier for each of a set of types. */
export type Appliers<T extends CommandType = CommandType> = {
  [K in T]: Applier<K>;
};

/**
 * The applier of every type of command. A type that the server registers
 * without an applier here fails the typecheck, as the generated union of
 * commands gains it, so that the page cannot leave a type out by mistake.
 */
export const appliers: Appliers = {
  ...boardAppliers,
  ...nodeAppliers,
  ...connectionAppliers,
  ...variableAppliers,
  ...componentAppliers,
  ...otherAppliers,
};

/** A command as an operation carries it, of a type that the page may not know, as one that a later server registers. */
export interface AnyCommand {
  type: string;
  payload: unknown;
}

/**
 * Applies commands, in order, to draft. A command of a type that the page
 * does not know changes nothing, and an applier that fails, as on a payload
 * that it does not expect, leaves what it changed not sure: in both, the
 * store reads what the batch changed from the server.
 */
export function applyCommands(
  draft: Draft,
  commands: readonly AnyCommand[],
): void {
  for (const command of commands) {
    const applier = Object.hasOwn(appliers, command.type)
      ? (appliers[command.type as CommandType] as Applier<CommandType>)
      : undefined;
    if (applier === undefined) {
      continue;
    }
    try {
      applier(draft, command.payload as PayloadOf<CommandType>);
    } catch {
      for (const id of draft.changes.keys()) {
        draft.changed(id, false);
      }
    }
  }
}

/**
 * Returns id as the server writes it, which takes an ID in either case, as a
 * payload of another client's may spell it, or id as it is if it is none.
 */
export function canonical(id: string): string;
export function canonical(id: string | undefined): string | undefined;
export function canonical(id: string | undefined): string | undefined {
  if (id === undefined) {
    return undefined;
  }
  try {
    return canonicalAnyId(id);
  } catch {
    return id;
  }
}
