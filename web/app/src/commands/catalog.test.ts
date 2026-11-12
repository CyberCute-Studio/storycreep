import { describe, expect, it } from "vitest";
import {
  type CommandId,
  commandIds,
  commandName,
  commandOf,
  commandOfKey,
} from "./catalog";
import {
  type Binding,
  bindingOn,
  type KeyInput,
  type Platform,
  platformOf,
} from "./keys";
import {
  type Combo,
  combosOf,
  keyOf,
  parseCombo,
  type ReservedPlatform,
  reservedLists,
} from "./reserved";

// The app's commands and their keys (PRD FR-EDIT-10, plan note N72): that
// each has a name and keys of its own, that none is a combination that a
// browser or a system of AC2 keeps for itself, but the few that mean what
// the system's own do, and how a key of each layout is read.

const platformsOf: Record<ReservedPlatform, Platform> = {
  windows: "windows",
  macos: "mac",
  chromeos: "chromeos",
};

/** A binding as the keys that it is on platform, of which Shift is either for a character that a layout types with Shift or without. */
interface Held {
  ctrl: boolean;
  meta: boolean;
  shift: boolean | "any";
  key: string;
}

function heldOf(binding: Binding, platform: Platform): Held {
  const mac = platform === "mac";
  const character =
    binding.code === undefined &&
    binding.key.length === 1 &&
    !/^[a-z0-9]$/.test(binding.key);
  return {
    ctrl: binding.ctrl === true || (binding.mod === true && !mac),
    meta: binding.mod === true && mac,
    shift:
      character || binding.shift === "any" ? "any" : binding.shift === true,
    key:
      binding.code?.startsWith("Digit") === true
        ? binding.code.slice(5)
        : keyOf(binding.key),
  };
}

function conflicts(held: Held, combo: Combo): boolean {
  return (
    !combo.alt &&
    combo.ctrl === held.ctrl &&
    combo.meta === held.meta &&
    (held.shift === "any" || combo.shift === held.shift) &&
    combo.key === held.key
  );
}

/**
 * The bindings that are a combination that a list keeps, each recorded
 * with why it is the app's all the same: what it does is what the
 * platform's own does, or the PRD names it.
 */
const allowed: ReadonlyMap<string, string> = new Map([
  [
    "palette.open Mod+k",
    "FR-EDIT-10 names Ctrl/Cmd+K, which Chrome, Edge, and Firefox focus their search box with",
  ],
  ["canvas.copy Mod+c", "copies, as the system's own does (N71)"],
  ["canvas.cut Mod+x", "cuts, as the system's own does (N71)"],
  ["canvas.paste Mod+v", "pastes, as the system's own does (N71)"],
  [
    "canvas.menu Shift+F10",
    "opens the context menu of what is selected, as Windows' and Edge's own does",
  ],
]);

function bindingKey(id: CommandId, binding: Binding): string {
  return [
    id,
    [
      binding.mod === true ? "Mod" : undefined,
      binding.ctrl === true ? "Ctrl" : undefined,
      binding.shift === true ? "Shift" : undefined,
      binding.key,
    ]
      .filter((part) => part !== undefined)
      .join("+"),
  ].join(" ");
}

/** The combinations of a platform's lists that a binding of on, acting outside text fields or not, may conflict with. */
function reservedFor(
  platform: ReservedPlatform,
  inText: boolean,
): { combo: Combo; of: string; text: string }[] {
  return reservedLists
    .filter((list) => list.platforms.includes(platform))
    .flatMap((list) => [
      ...combosOf(list.anywhere).map((text) => ({
        combo: parseCombo(text),
        of: list.of,
        text,
      })),
      ...(inText
        ? combosOf(list.text).map((text) => ({
            combo: parseCombo(text),
            of: list.of,
            text,
          }))
        : []),
    ]);
}

function key(e: Partial<KeyInput> & { key: string }): KeyInput {
  return {
    code: "",
    shiftKey: false,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    ...e,
  };
}

describe("the commands", () => {
  // Verifies: FR-EDIT-10/AC1
  it("each has a name of its own, and no two have the same key where both act", () => {
    const t = ((k: string) => k) as Parameters<typeof commandName>[0];
    const names = commandIds.map((id) => commandName(t, id));
    expect(new Set(names).size).toBe(commandIds.length);
    for (const platform of ["mac", "windows", "chromeos", "linux"] as const) {
      const seen = new Map<string, CommandId>();
      for (const id of commandIds) {
        const spec = commandOf(id);
        for (const binding of spec.keys) {
          if (!bindingOn(binding, platform)) {
            continue;
          }
          const held = heldOf(binding, platform);
          const at = `${spec.on} ${held.ctrl} ${held.meta} ${held.shift} ${held.key}`;
          expect(seen.get(at), `${id} on ${platform}`).toBeUndefined();
          seen.set(at, id);
        }
      }
    }
  });

  // Verifies: FR-EDIT-10/AC2
  it("have no key that a browser or a system of Windows, macOS, or ChromeOS keeps, but those that mean the same", () => {
    const found: string[] = [];
    const used = new Set<string>();
    for (const platform of ["windows", "macos", "chromeos"] as const) {
      for (const id of commandIds) {
        const spec = commandOf(id);
        // A key of the canvas, or one that acts outside text fields alone,
        // as the undo, is never a key of a field that the user types in.
        const inText = spec.on === "page" && spec.outsideText !== true;
        for (const binding of spec.keys) {
          if (!bindingOn(binding, platformsOf[platform])) {
            continue;
          }
          const held = heldOf(binding, platformsOf[platform]);
          for (const r of reservedFor(platform, inText)) {
            if (!conflicts(held, r.combo)) {
              continue;
            }
            const k = bindingKey(id, binding);
            if (allowed.has(k)) {
              used.add(k);
            } else {
              found.push(`${k} is ${r.text} of ${r.of} on ${platform}`);
            }
          }
        }
      }
    }
    expect(found).toEqual([]);
    // Every exception is of a binding that conflicts, so that the list
    // holds no more than it must.
    expect([...used].sort()).toEqual([...allowed.keys()].sort());
  });

  it("are checked against lists that hold only combinations that a page cannot take as typing", () => {
    for (const list of reservedLists) {
      for (const text of [...combosOf(list.anywhere), ...combosOf(list.text)]) {
        const combo = parseCombo(text);
        const modified = combo.ctrl || combo.alt || combo.meta;
        const named = combo.key.length > 1;
        // A function key, or a key held with Ctrl, Alt, Cmd, Win, or
        // Search, or Shift with a key that types nothing, as Escape.
        expect(
          modified || /^f\d+$/.test(combo.key) || (combo.shift && named),
          `${text} of ${list.of}`,
        ).toBe(true);
      }
    }
  });
});

describe("a key", () => {
  it("is the command of the letter that it types, or of its place where the layout's letters are not Latin", () => {
    // Czech QWERTZ: the key that types z is where Y is on QWERTY.
    expect(
      commandOfKey(
        key({ key: "z", code: "KeyY", ctrlKey: true }),
        "page",
        "windows",
      ),
    ).toBe("history.undo");
    // Russian: the key of Z types я.
    expect(
      commandOfKey(
        key({ key: "я", code: "KeyZ", ctrlKey: true }),
        "page",
        "windows",
      ),
    ).toBe("history.undo");
    expect(
      commandOfKey(key({ key: "e", code: "KeyE" }), "canvas", "windows"),
    ).toBe("canvas.add_element");
    // Russian: the key of M types ь, a letter of its own script.
    expect(
      commandOfKey(key({ key: "ь", code: "KeyM" }), "canvas", "windows"),
    ).toBe("canvas.minimap");
    // A key whose place is a letter's, but which types what is no letter,
    // is no letter's: the , of French AZERTY's key of M, the . of Dvorak's
    // key of E, and its ; of the key of Z, held with Ctrl.
    expect(
      commandOfKey(key({ key: ",", code: "KeyM" }), "canvas", "windows"),
    ).toBeUndefined();
    expect(
      commandOfKey(key({ key: ".", code: "KeyE" }), "canvas", "windows"),
    ).toBeUndefined();
    expect(
      commandOfKey(
        key({ key: ";", code: "KeyZ", ctrlKey: true }),
        "page",
        "windows",
      ),
    ).toBeUndefined();
    // Shift and a letter is no command of the canvas.
    expect(
      commandOfKey(
        key({ key: "E", code: "KeyE", shiftKey: true }),
        "canvas",
        "windows",
      ),
    ).toBeUndefined();
  });

  it("takes Cmd on macOS and Ctrl elsewhere, and neither with Alt, as AltGr is", () => {
    const k = key({ key: "k", code: "KeyK", ctrlKey: true });
    expect(commandOfKey(k, "page", "windows")).toBe("palette.open");
    expect(commandOfKey(k, "page", "mac")).toBeUndefined();
    expect(
      commandOfKey({ ...k, ctrlKey: false, metaKey: true }, "page", "mac"),
    ).toBe("palette.open");
    expect(
      commandOfKey({ ...k, altKey: true }, "page", "windows"),
    ).toBeUndefined();
  });

  it("redoes with Ctrl+Y on Windows, Ctrl+Shift+Z on ChromeOS, and Cmd+Shift+Z on macOS", () => {
    expect(
      commandOfKey(
        key({ key: "y", code: "KeyY", ctrlKey: true }),
        "page",
        "windows",
      ),
    ).toBe("history.redo");
    const shiftZ = key({ key: "Z", code: "KeyZ", shiftKey: true });
    expect(
      commandOfKey({ ...shiftZ, ctrlKey: true }, "page", "windows"),
    ).toBeUndefined();
    expect(commandOfKey({ ...shiftZ, ctrlKey: true }, "page", "chromeos")).toBe(
      "history.redo",
    );
    expect(commandOfKey({ ...shiftZ, metaKey: true }, "page", "mac")).toBe(
      "history.redo",
    );
  });

  it("fits with Shift+1 by its place before what it types, and zooms by the character", () => {
    // Swiss: Shift and 1 types +.
    expect(
      commandOfKey(
        key({ key: "+", code: "Digit1", shiftKey: true }),
        "canvas",
        "windows",
      ),
    ).toBe("canvas.fit");
    // Czech: 1 types + without Shift, and 0 types é.
    expect(
      commandOfKey(key({ key: "+", code: "Digit1" }), "canvas", "windows"),
    ).toBe("canvas.zoom_in");
    expect(
      commandOfKey(key({ key: "é", code: "Digit0" }), "canvas", "windows"),
    ).toBe("canvas.zoom_reset");
    expect(
      commandOfKey(
        key({ key: "?", code: "Slash", shiftKey: true }),
        "canvas",
        "windows",
      ),
    ).toBe("shortcuts.show");
  });

  it("opens the menu with Shift+F10 or the menu key, and not with F10 alone", () => {
    expect(
      commandOfKey(
        key({ key: "F10", code: "F10", shiftKey: true }),
        "canvas",
        "windows",
      ),
    ).toBe("canvas.menu");
    expect(
      commandOfKey(key({ key: "F10", code: "F10" }), "canvas", "windows"),
    ).toBeUndefined();
    expect(
      commandOfKey(
        key({ key: "ContextMenu", code: "ContextMenu" }),
        "canvas",
        "windows",
      ),
    ).toBe("canvas.menu");
  });

  it("is of the platform that the browser says it runs on", () => {
    expect(platformOf({ platform: "MacIntel", userAgent: "" })).toBe("mac");
    expect(platformOf({ platform: "Win32", userAgent: "" })).toBe("windows");
    expect(
      platformOf({
        platform: "Linux x86_64",
        userAgent: "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0)",
      }),
    ).toBe("chromeos");
    expect(platformOf({ platform: "Linux x86_64", userAgent: "" })).toBe(
      "linux",
    );
  });
});
