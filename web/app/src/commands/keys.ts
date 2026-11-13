// The keys of the app's shortcuts (PRD FR-EDIT-10, plan note N72): what a
// binding of a command is, which key of a keyboard's event it is, on which
// platform, and how the shortcuts' table writes it.

import type { TFunction } from "i18next";

/**
 * A platform whose browsers and system keep keys of their own (FR-EDIT-10
 * AC2), which the shortcuts follow: macOS, whose Cmd is the others' Ctrl,
 * Windows, ChromeOS, and Linux.
 */
export type Platform = "mac" | "windows" | "chromeos" | "linux";

/** What a key's event tells of the key, as KeyboardEvent has it. */
export interface KeyInput {
  key: string;
  code: string;
  shiftKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
}

/**
 * A key of a command's shortcut, with the modifiers held with it. None is
 * held with Alt, which types a character of its own on macOS, and is a
 * layout's AltGr, as Ctrl and Alt are on Windows.
 */
export interface Binding {
  /**
   * The key: a Latin letter, in lower case, which is matched by the letter
   * that the key types, or, on a layout whose letters are not Latin, as
   * Cyrillic's, by its place, as the browser's own shortcuts go; a digit,
   * by the digit that it types, or its place, as 0 is on a Czech layout,
   * which types é there; another character, as + or ?, by the character
   * alone, with Shift or without, whichever the layout types it with; or
   * a key's name, as KeyboardEvent.key names it, as ArrowLeft.
   */
  key: string;
  /**
   * The key's place, as KeyboardEvent.code names it, which the binding is
   * matched by in place of key: Shift and 1, whose character is ! or + or
   * another, on another layout, before what it types.
   */
  code?: string;
  /** Cmd on macOS, and Ctrl on the others. */
  mod?: boolean;
  /** Ctrl on every platform. */
  ctrl?: boolean;
  /**
   * Shift: held, or not held; or either, as of a key that Shift does not
   * change the action of, as Escape. A character other than a letter or a
   * digit is typed with Shift or without, as its layout has it, and its
   * binding says nothing of Shift.
   */
  shift?: boolean | "any";
  /** The platforms that the binding is a key of, or every one. */
  platforms?: readonly Platform[];
}

/** Returns the platform of the browser of nav, as its system is, which its shortcuts follow. */
export function platformOf(
  nav: Pick<Navigator, "platform" | "userAgent"> = navigator,
): Platform {
  // An iPad's Safari says that it is a Mac, whose keys a keyboard of the
  // iPad's has, with Cmd.
  if (/Mac|iPhone|iPad|iPod/.test(nav.platform)) {
    return "mac";
  }
  if (/\bCrOS\b/.test(nav.userAgent)) {
    return "chromeos";
  }
  if (/Win/.test(nav.platform)) {
    return "windows";
  }
  return "linux";
}

/** Whether key is a Latin letter, as a binding's key. */
function isLetter(key: string): boolean {
  return /^[a-z]$/.test(key);
}

/** Whether key is a digit, as a binding's key. */
function isDigit(key: string): boolean {
  return /^[0-9]$/.test(key);
}

/**
 * Returns the Latin letter that the key of e types, in lower case, or, of a
 * key that types a letter of another script, as Cyrillic's я, the letter of
 * its place, or undefined for a key that types none. A key that types what
 * is no letter is the key of no letter, whatever its place: the key of M
 * types , on a French layout, and the key of E types . on Dvorak, neither
 * of which is M or E there, as the browser's own shortcuts go by.
 */
export function letterOf(e: KeyInput): string | undefined {
  if (/^[a-z]$/i.test(e.key)) {
    return e.key.toLowerCase();
  }
  return /^\p{L}$/u.test(e.key) &&
    !/^\p{Script=Latin}$/u.test(e.key) &&
    /^Key[A-Z]$/.test(e.code)
    ? e.code.slice(3).toLowerCase()
    : undefined;
}

/** Reports whether binding applies on platform. */
export function bindingOn(binding: Binding, platform: Platform): boolean {
  return binding.platforms?.includes(platform) ?? true;
}

/** Reports whether the key of e is binding, on platform. */
export function matches(
  binding: Binding,
  e: KeyInput,
  platform: Platform,
): boolean {
  // A modifier that the event does not say of is not held.
  if (!bindingOn(binding, platform) || e.altKey === true) {
    return false;
  }
  const mac = platform === "mac";
  const ctrl = binding.ctrl === true || (binding.mod === true && !mac);
  const meta = binding.mod === true && mac;
  if ((e.ctrlKey === true) !== ctrl || (e.metaKey === true) !== meta) {
    return false;
  }
  const shift = (e.shiftKey === true) === (binding.shift === true);
  if (binding.code !== undefined) {
    return e.code === binding.code && shift;
  }
  const key = binding.key;
  if (isLetter(key)) {
    return letterOf(e) === key && shift;
  }
  if (isDigit(key)) {
    return (e.key === key || e.code === `Digit${key}`) && shift;
  }
  if (key.length === 1) {
    return e.key === key;
  }
  return e.key === key && (binding.shift === "any" || shift);
}

/**
 * Reports whether element is a field that the user types text in, whose
 * keys are its own, as Ctrl and Z that undoes what was typed in it: a text
 * input, a text area, or an editable element, as the rich-text editor.
 */
export function isTextField(element: EventTarget | null): boolean {
  if (!(element instanceof HTMLElement)) {
    return false;
  }
  if (element instanceof HTMLTextAreaElement || element.isContentEditable) {
    return true;
  }
  return (
    element instanceof HTMLInputElement &&
    ![
      "button",
      "checkbox",
      "color",
      "file",
      "hidden",
      "image",
      "radio",
      "range",
      "reset",
      "submit",
    ].includes(element.type)
  );
}

/** Returns the name of a key that the shortcuts' table writes, as Escape, or the key as it is, as a letter, in capitals. */
function keyName(t: TFunction, key: string): string {
  switch (key) {
    case "ArrowLeft":
      return t("keys.arrow_left");
    case "ArrowRight":
      return t("keys.arrow_right");
    case "ArrowUp":
      return t("keys.arrow_up");
    case "ArrowDown":
      return t("keys.arrow_down");
    case "Escape":
      return t("keys.escape");
    case "Enter":
      return t("keys.enter");
    case "Delete":
      return t("keys.delete");
    case "Backspace":
      return t("keys.backspace");
    case "ContextMenu":
      return t("keys.menu");
    case "-":
      return "−";
  }
  return key.toUpperCase();
}

/** The names that the parts of a binding are written by: its modifiers', and its key's, by the key. */
interface PartNames {
  cmd: string;
  ctrl: string;
  shift: string;
  key: (key: string) => string;
}

/**
 * Returns the parts of binding on platform, by names, each a key, the
 * modifiers first: Cmd, which is Ctrl but on macOS, Ctrl, Shift, and the
 * key, or the digit of a key of a digit's place (bindingParts,
 * ariaKeyShortcuts).
 */
function partsOf(
  binding: Binding,
  platform: Platform,
  names: PartNames,
): string[] {
  const parts: string[] = [];
  if (binding.mod === true) {
    parts.push(platform === "mac" ? names.cmd : names.ctrl);
  }
  if (binding.ctrl === true) {
    parts.push(names.ctrl);
  }
  if (binding.shift === true) {
    parts.push(names.shift);
  }
  parts.push(
    binding.code?.startsWith("Digit") === true
      ? binding.code.slice(5)
      : names.key(binding.key),
  );
  return parts;
}

/**
 * Returns the parts of binding as the shortcuts' table and the palette
 * write them on platform, each a key, the modifiers first: Cmd, or Ctrl,
 * Shift, and the key.
 */
export function bindingParts(
  t: TFunction,
  binding: Binding,
  platform: Platform,
): string[] {
  return partsOf(binding, platform, {
    cmd: t("keys.cmd"),
    ctrl: t("keys.ctrl"),
    shift: t("keys.shift"),
    key: (key) => keyName(t, key),
  });
}

/** Returns binding as one string, its keys joined by +, as Ctrl+K, which a text says, as the canvas's help. */
export function bindingText(
  t: TFunction,
  binding: Binding,
  platform: Platform,
): string {
  return bindingParts(t, binding, platform).join("+");
}

/**
 * Returns bindings as a control's aria-keyshortcuts says them, on platform,
 * which assistive technology is told of, as Control+K, or Meta+K on macOS:
 * each that applies there, its modifiers by the names that ARIA gives
 * them, and its key, a letter in capitals, separated by spaces; or
 * undefined where none applies.
 */
export function ariaKeyShortcuts(
  bindings: readonly Binding[],
  platform: Platform,
): string | undefined {
  const shortcuts = bindings
    .filter((binding) => bindingOn(binding, platform))
    .map((binding) =>
      partsOf(binding, platform, {
        cmd: "Meta",
        ctrl: "Control",
        shift: "Shift",
        key: (key) => (key.length === 1 ? key.toUpperCase() : key),
      }).join("+"),
    );
  return shortcuts.length === 0 ? undefined : shortcuts.join(" ");
}
