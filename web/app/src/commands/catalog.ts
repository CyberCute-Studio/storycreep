// The app's commands (PRD FR-EDIT-10, plan note N72): every action that the
// command palette lists, and that a shortcut runs, by one ID, which the
// palette, the table of the shortcuts, and the keys share (AC1). A command
// is run by what a page of the app registers for it while it is shown
// (registry.tsx), as the canvas's zoom, so the palette lists what the page
// shown can do. Remapping the keys is plan item 129's, and play mode's
// commands item 71's.

import type { TFunction } from "i18next";
import type { Binding, KeyInput, Platform } from "./keys";
import { matches } from "./keys";

/** The groups of the table of the shortcuts, in its order, as FR-EDIT-10 lists them. */
export type Group =
  | "general"
  | "search"
  | "history"
  | "navigation"
  | "view"
  | "creation"
  | "connection"
  | "editing";

/** The groups in the order that the table shows them. */
export const groups: readonly Group[] = [
  "general",
  "search",
  "history",
  "navigation",
  "view",
  "creation",
  "connection",
  "editing",
];

/** A command: its group, its keys, and where they act. */
export interface CommandSpec {
  group: Group;
  /** The keys of its shortcut, of which any runs it; none for a command of the palette alone. */
  keys: readonly Binding[];
  /**
   * Where its keys act: anywhere on the page, wherever the focus is, but in
   * a modal dialog; or on the board's canvas, while it has the focus, as
   * its letters do, which a key acts as only on the canvas (WCAG 2.1.4).
   */
  on: "page" | "canvas";
  /**
   * Whether its keys act only where the focus is not in a field that the
   * user types in, whose own they are there, as the field's undo is.
   */
  outsideText?: boolean;
  /**
   * Whether a key of it held down runs it again with each of the repeats
   * that the system sends, as an arrow pans on and Ctrl+Z undoes on; one
   * that does not runs once for a key held down, as E adds one element
   * rather than one with each repeat, and the palette's key opens the
   * palette rather than open and close it by turns.
   */
  repeats?: boolean;
}

const page = "page" as const;
const canvas = "canvas" as const;

/** The key of a letter on the canvas. */
const letter = (key: string): Binding => ({ key });

/** Every command, in the order of the palette and the table. */
export const commands = {
  "palette.open": {
    group: "general",
    on: page,
    keys: [{ mod: true, key: "k" }],
  },
  "shortcuts.show": { group: "general", on: canvas, keys: [{ key: "?" }] },
  // Ctrl+/ on every platform, macOS's too, where Cmd+/ is Safari's Show
  // Status Bar, and no browser's or system's list gives Ctrl+/.
  "project.search": {
    group: "search",
    on: page,
    keys: [{ ctrl: true, key: "/" }],
  },
  "history.undo": {
    group: "history",
    on: page,
    outsideText: true,
    repeats: true,
    keys: [{ mod: true, key: "z" }],
  },
  // Redo is Cmd+Shift+Z on macOS, and ChromeOS's Ctrl+Shift+Z, which
  // Firefox on Windows and Linux opens its debugger with: Ctrl+Y there.
  "history.redo": {
    group: "history",
    on: page,
    outsideText: true,
    repeats: true,
    keys: [
      { mod: true, shift: true, key: "z", platforms: ["mac", "chromeos"] },
      { ctrl: true, key: "y", platforms: ["windows", "linux"] },
    ],
  },
  "go.projects": { group: "navigation", on: page, keys: [] },
  "go.account": { group: "navigation", on: page, keys: [] },
  "go.boards": { group: "navigation", on: page, keys: [] },
  "go.variables": { group: "navigation", on: page, keys: [] },
  "go.components": { group: "navigation", on: page, keys: [] },
  "go.component_types": { group: "navigation", on: page, keys: [] },
  "go.statistics": { group: "navigation", on: page, keys: [] },
  "go.validation": { group: "navigation", on: page, keys: [] },
  "go.dependencies": { group: "navigation", on: page, keys: [] },
  "go.trash": { group: "navigation", on: page, keys: [] },
  "go.access": { group: "navigation", on: page, keys: [] },
  "go.settings": { group: "navigation", on: page, keys: [] },
  "canvas.settings": { group: "navigation", on: canvas, keys: [] },
  "canvas.go_to_target": {
    group: "navigation",
    on: canvas,
    keys: [letter("g")],
  },
  // The board's page's views, its canvas and its script view (plan item 68).
  "board.show_canvas": { group: "view", on: page, keys: [] },
  "board.show_script": { group: "view", on: page, keys: [] },
  "canvas.pan_left": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "ArrowLeft" }],
  },
  "canvas.pan_right": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "ArrowRight" }],
  },
  "canvas.pan_up": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "ArrowUp" }],
  },
  "canvas.pan_down": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "ArrowDown" }],
  },
  "canvas.pan_far_left": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "ArrowLeft", shift: true }],
  },
  "canvas.pan_far_right": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "ArrowRight", shift: true }],
  },
  "canvas.pan_far_up": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "ArrowUp", shift: true }],
  },
  "canvas.pan_far_down": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "ArrowDown", shift: true }],
  },
  "canvas.zoom_in": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "+" }, { key: "=" }],
  },
  "canvas.zoom_out": {
    group: "view",
    on: canvas,
    repeats: true,
    keys: [{ key: "-" }, { key: "_" }],
  },
  "canvas.zoom_reset": { group: "view", on: canvas, keys: [{ key: "0" }] },
  // By the key's place, before what it types: Shift and 1 types + on a
  // Swiss layout, where = and the keypad's + zoom in still.
  "canvas.fit": {
    group: "view",
    on: canvas,
    keys: [{ key: "1", code: "Digit1", shift: true }],
  },
  "canvas.zoom_selection": {
    group: "view",
    on: canvas,
    keys: [{ key: "2", code: "Digit2", shift: true }],
  },
  "canvas.minimap": { group: "view", on: canvas, keys: [letter("m")] },
  "canvas.mode_pan": { group: "view", on: canvas, keys: [letter("h")] },
  "canvas.mode_select": { group: "view", on: canvas, keys: [letter("v")] },
  "canvas.add_element": {
    group: "creation",
    on: canvas,
    keys: [letter("e")],
  },
  "canvas.add_branch": { group: "creation", on: canvas, keys: [letter("b")] },
  "canvas.add_jumper": { group: "creation", on: canvas, keys: [letter("j")] },
  "canvas.duplicate": { group: "creation", on: canvas, keys: [letter("d")] },
  // The system's own keys of the clipboard, which copy, cut, and paste
  // nodes where they would text (plan note N71).
  "canvas.copy": {
    group: "creation",
    on: canvas,
    keys: [{ mod: true, key: "c" }],
  },
  "canvas.cut": {
    group: "creation",
    on: canvas,
    keys: [{ mod: true, key: "x" }],
  },
  "canvas.paste": {
    group: "creation",
    on: canvas,
    keys: [{ mod: true, key: "v" }],
  },
  "canvas.connect": { group: "connection", on: canvas, keys: [letter("c")] },
  "canvas.open": {
    group: "editing",
    on: canvas,
    keys: [{ key: "Enter", shift: "any" }],
  },
  "canvas.delete": {
    group: "editing",
    on: canvas,
    keys: [
      { key: "Delete", shift: "any" },
      { key: "Backspace", shift: "any" },
    ],
  },
  // Without Shift: Shift+Escape is Chrome's task manager, and Firefox's,
  // which ends a drag of the canvas still, leaving the key to the browser
  // (canvas/keyboard.ts's escapeWithShift).
  "canvas.deselect": {
    group: "editing",
    on: canvas,
    keys: [{ key: "Escape" }],
  },
  "canvas.menu": {
    group: "editing",
    on: canvas,
    keys: [
      { key: "F10", shift: true },
      { key: "ContextMenu", shift: "any" },
    ],
  },
  "canvas.inspector": { group: "editing", on: canvas, keys: [letter("i")] },
  "canvas.snap": { group: "editing", on: canvas, keys: [letter("s")] },
} as const satisfies Record<string, CommandSpec>;

/** The ID of a command, which the palette, the table, and the keys share. */
export type CommandId = keyof typeof commands;

/** Every command's ID, in the order of the palette and the table. */
export const commandIds = Object.keys(commands) as CommandId[];

/** Returns the command of id. */
export function commandOf(id: CommandId): CommandSpec {
  return commands[id];
}

/** Returns the commands that have keys, which act where on, in the catalog's order. */
function keyedOn(on: CommandSpec["on"]): readonly CommandId[] {
  return commandIds.filter(
    (id) => commands[id].on === on && commands[id].keys.length > 0,
  );
}

/** The commands that have keys on the page, and on the canvas, which each key that goes down is looked up in (commandOfKey). */
const commandsOn: Readonly<Record<CommandSpec["on"], readonly CommandId[]>> = {
  page: keyedOn(page),
  canvas: keyedOn(canvas),
};

/**
 * Returns the command whose keys act where on, and that the key of e is,
 * on platform, or undefined for a key of none: the bindings by a key's
 * place first, as Shift and 1, which fits the content whatever the
 * layout's character of it, as + on a Swiss layout, which zooms in
 * otherwise.
 */
export function commandOfKey(
  e: KeyInput,
  on: CommandSpec["on"],
  platform: Platform,
): CommandId | undefined {
  const ids = commandsOn[on];
  const byPlace = ids.find((id) =>
    commands[id].keys.some(
      (b: Binding) => b.code !== undefined && matches(b, e, platform),
    ),
  );
  return (
    byPlace ??
    ids.find((id) =>
      commands[id].keys.some(
        (b: Binding) => b.code === undefined && matches(b, e, platform),
      ),
    )
  );
}

/** Returns the name of the command of id, as the palette and the table say it. */
export function commandName(t: TFunction, id: CommandId): string {
  switch (id) {
    case "palette.open":
      return t("command.palette.open");
    case "shortcuts.show":
      return t("command.shortcuts.show");
    case "project.search":
      return t("command.project.search");
    case "history.undo":
      return t("command.history.undo");
    case "history.redo":
      return t("command.history.redo");
    case "go.projects":
      return t("command.go.projects");
    case "go.account":
      return t("command.go.account");
    case "go.boards":
      return t("command.go.boards");
    case "go.variables":
      return t("command.go.variables");
    case "go.components":
      return t("command.go.components");
    case "go.component_types":
      return t("command.go.component_types");
    case "go.statistics":
      return t("command.go.statistics");
    case "go.validation":
      return t("command.go.validation");
    case "go.dependencies":
      return t("command.go.dependencies");
    case "go.trash":
      return t("command.go.trash");
    case "go.access":
      return t("command.go.access");
    case "go.settings":
      return t("command.go.settings");
    case "canvas.settings":
      return t("command.canvas.settings");
    case "canvas.go_to_target":
      return t("command.canvas.go_to_target");
    case "board.show_canvas":
      return t("command.board.show_canvas");
    case "board.show_script":
      return t("command.board.show_script");
    case "canvas.pan_left":
      return t("command.canvas.pan_left");
    case "canvas.pan_right":
      return t("command.canvas.pan_right");
    case "canvas.pan_up":
      return t("command.canvas.pan_up");
    case "canvas.pan_down":
      return t("command.canvas.pan_down");
    case "canvas.pan_far_left":
      return t("command.canvas.pan_far_left");
    case "canvas.pan_far_right":
      return t("command.canvas.pan_far_right");
    case "canvas.pan_far_up":
      return t("command.canvas.pan_far_up");
    case "canvas.pan_far_down":
      return t("command.canvas.pan_far_down");
    case "canvas.zoom_in":
      return t("command.canvas.zoom_in");
    case "canvas.zoom_out":
      return t("command.canvas.zoom_out");
    case "canvas.zoom_reset":
      return t("command.canvas.zoom_reset");
    case "canvas.fit":
      return t("command.canvas.fit");
    case "canvas.zoom_selection":
      return t("command.canvas.zoom_selection");
    case "canvas.minimap":
      return t("command.canvas.minimap");
    case "canvas.mode_pan":
      return t("command.canvas.mode_pan");
    case "canvas.mode_select":
      return t("command.canvas.mode_select");
    case "canvas.add_element":
      return t("command.canvas.add_element");
    case "canvas.add_branch":
      return t("command.canvas.add_branch");
    case "canvas.add_jumper":
      return t("command.canvas.add_jumper");
    case "canvas.duplicate":
      return t("command.canvas.duplicate");
    case "canvas.copy":
      return t("command.canvas.copy");
    case "canvas.cut":
      return t("command.canvas.cut");
    case "canvas.paste":
      return t("command.canvas.paste");
    case "canvas.connect":
      return t("command.canvas.connect");
    case "canvas.open":
      return t("command.canvas.open");
    case "canvas.delete":
      return t("command.canvas.delete");
    case "canvas.deselect":
      return t("command.canvas.deselect");
    case "canvas.menu":
      return t("command.canvas.menu");
    case "canvas.inspector":
      return t("command.canvas.inspector");
    case "canvas.snap":
      return t("command.canvas.snap");
  }
}

/** Returns the name of group, as the table heads it. */
export function groupName(t: TFunction, group: Group): string {
  switch (group) {
    case "general":
      return t("shortcuts.group.general");
    case "search":
      return t("shortcuts.group.search");
    case "history":
      return t("shortcuts.group.history");
    case "navigation":
      return t("shortcuts.group.navigation");
    case "view":
      return t("shortcuts.group.view");
    case "creation":
      return t("shortcuts.group.creation");
    case "connection":
      return t("shortcuts.group.connection");
    case "editing":
      return t("shortcuts.group.editing");
  }
}
