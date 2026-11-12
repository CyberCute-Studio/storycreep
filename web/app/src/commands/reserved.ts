// The key combinations that the browsers and the systems of PRD §9.12 keep
// for themselves (FR-EDIT-10 AC2, plan note N72), which no shortcut of the
// app may be, from their own lists of shortcuts, read on 2026-11-12: each
// combination that one of them acts on while a page has the focus. What
// acts only in one application of a system, as Finder's, File Explorer's,
// or ChromeOS's Text's, or only in a browser's address bar, or its PDF
// viewer, is left out, as it never acts on a page. So is a key held with
// Shift alone, or none, but a function key, and Shift with Escape, whose
// browsers' own action, as Space's and the arrows' that scroll, or
// Escape's that stops a load, is the default of the element that has the
// focus, which a control takes over, as every control of ARIA's patterns
// does with the arrows, Space, and Escape. A combination that acts only in
// a field that the user types in, as Ctrl and Z, which undoes what was
// typed, is listed apart (text), as it does not keep a shortcut that acts
// only outside such a field, as the app's undo, nor one of the canvas.
//
// A combination is written as its lists write it: its modifiers, Ctrl,
// Alt, which is macOS's Option, Shift, Cmd, Win, and Search, which is
// ChromeOS's launcher key, and its key, a letter, a digit, a character,
// Plus for +, or a key's name, joined by +.

/** A platform that FR-EDIT-10 AC2 names. */
export type ReservedPlatform = "windows" | "macos" | "chromeos";

/** The combinations of one list of a browser's or a system's shortcuts. */
export interface ReservedList {
  /** Whose list it is, and of which platform. */
  of: string;
  /** Where it is published. */
  source: string;
  /** The platforms that it keeps the combinations on. */
  platforms: readonly ReservedPlatform[];
  /** The combinations that act wherever the page's focus is, separated by spaces. */
  anywhere: string;
  /** The combinations that act only in a field that the user types in, separated by spaces. */
  text?: string;
}

const chromeHelp = "https://support.google.com/chrome/answer/157179";

export const reservedLists: readonly ReservedList[] = [
  {
    // Chrome on ChromeOS is Chrome on Linux, whose keys it has as well as
    // those that ChromeOS's own list gives.
    of: "Chrome, Windows and Linux; Edge, of the same engine",
    source: chromeHelp,
    platforms: ["windows", "chromeos"],
    anywhere: `Ctrl+N Ctrl+Shift+N Ctrl+T Ctrl+Shift+T Ctrl+Tab Ctrl+PageDown
      Ctrl+Shift+Tab Ctrl+PageUp Ctrl+1 Ctrl+2 Ctrl+3 Ctrl+4 Ctrl+5 Ctrl+6
      Ctrl+7 Ctrl+8 Ctrl+9 Alt+Home Alt+Left Alt+Right Ctrl+W Ctrl+F4
      Ctrl+Shift+W Alt+F4 Alt+Space Alt+F Alt+E Ctrl+Shift+PageUp
      Ctrl+Shift+PageDown F11 F6 Ctrl+Shift+H Ctrl+Shift+B Ctrl+Shift+O
      Ctrl+H Ctrl+J Shift+Escape Alt+Shift+T F10 Ctrl+F F3 Ctrl+G
      Ctrl+Shift+G Ctrl+Shift+J F12 Ctrl+Shift+I Ctrl+Shift+C
      Ctrl+Shift+Delete F1 Ctrl+Shift+M Alt+Shift+I F7 Ctrl+F6 Alt+Shift+A
      Alt+Shift+N Ctrl+L Alt+D Ctrl+K Ctrl+E Ctrl+F5 Ctrl+P Ctrl+S F5
      Ctrl+R Shift+F5 Ctrl+Shift+R Ctrl+O Ctrl+U Ctrl+D Ctrl+Shift+D
      Ctrl+Plus Ctrl+= Ctrl+- Ctrl+0`,
    text: "Ctrl+Left Ctrl+Right Ctrl+Backspace",
  },
  {
    of: "Chrome, macOS; Edge, of the same engine",
    source: chromeHelp,
    platforms: ["macos"],
    anywhere: `Cmd+N Cmd+Shift+N Cmd+T Cmd+Shift+T Cmd+Alt+Right Cmd+Alt+Left
      Cmd+1 Cmd+2 Cmd+3 Cmd+4 Cmd+5 Cmd+6 Cmd+7 Cmd+8 Cmd+9 Cmd+[ Cmd+Left
      Cmd+] Cmd+Right Cmd+W Cmd+Shift+W Cmd+M Cmd+H Cmd+Q
      Ctrl+Shift+PageUp Ctrl+Shift+PageDown Ctrl+F2 Cmd+Alt+Up Cmd+Alt+Down
      F7 Cmd+Shift+B Cmd+Alt+B Cmd+, Cmd+Y Cmd+Shift+J Cmd+Alt+I Cmd+Alt+J
      Cmd+Alt+U Cmd+Alt+C Cmd+F Cmd+G Cmd+Shift+G Cmd+E Cmd+Alt+F
      Cmd+Shift+Delete Cmd+Shift+M Cmd+Alt+Shift+I Cmd+Alt+Shift+A
      Cmd+Alt+N Cmd+Shift+I Cmd+D Cmd+Shift+D Cmd+P Cmd+Alt+P Cmd+S Cmd+O
      Cmd+R Cmd+Shift+R Cmd+= Cmd+Plus Cmd+- Cmd+0 Cmd+Shift+H Cmd+L
      Ctrl+F5 Ctrl+Cmd+F`,
    text: "Alt+Left Alt+Right Alt+Backspace",
  },
  {
    of: "ChromeOS",
    source: "https://support.google.com/chromebook/answer/183101",
    platforms: ["chromeos"],
    anywhere: `Ctrl+N Ctrl+Shift+N Ctrl+T Ctrl+O Ctrl+W Ctrl+Shift+W
      Ctrl+Shift+T Ctrl+1 Ctrl+2 Ctrl+3 Ctrl+4 Ctrl+5 Ctrl+6 Ctrl+7 Ctrl+8
      Ctrl+9 Ctrl+Tab Ctrl+Shift+Tab Alt+Tab Alt+Shift+Tab Alt+Left
      Alt+Right Alt+[ Alt+] Alt+= Alt+- Search+Alt+M Alt+Up Alt+Down
      Ctrl+Alt+Up Ctrl+Alt+Down Ctrl+P Ctrl+S Ctrl+R Ctrl+Shift+R Ctrl+Plus
      Ctrl+= Ctrl+- Ctrl+0 Ctrl+F Ctrl+G Ctrl+Shift+G Alt+Shift+A Ctrl+K
      Ctrl+E Ctrl+U Ctrl+Shift+I Ctrl+Shift+C Ctrl+Shift+J Ctrl+Shift+B
      Alt+Shift+T Ctrl+H Ctrl+J Ctrl+D Ctrl+Shift+D Alt+Shift+M Alt+Shift+S
      Alt+1 Alt+2 Alt+3 Alt+4 Alt+5 Alt+6 Alt+7 Alt+8 Alt+9 Alt+Shift+N
      Ctrl+Shift+Plus Ctrl+Shift+= Ctrl+Shift+- Ctrl+Shift+0 Ctrl+Alt+.
      Ctrl+Alt+, Search+Alt Ctrl+A Ctrl+L Alt+D Ctrl+X Ctrl+C Ctrl+V
      Search+V Ctrl+Shift+Space Ctrl+Space Search+Shift+Space Ctrl+Alt+Z
      Search+Ctrl+H Search+Ctrl+M Search+Ctrl+D Ctrl+Alt+Left Ctrl+Alt+Right
      Alt+Shift+L Alt+Shift+B Search+L Search+Ctrl+S Ctrl+Shift+Q
      Search+Shift+= Search+[ Search+] Search+Shift+[ Search+Shift+]
      Search+Shift+A Search+F1 Search+F2 Search+F3 Search+F4 Search+F5
      Search+F6 Search+F7 Search+F8 Search+F9 Search+F10 Search+F11
      Search+F12`,
    text: `Ctrl+Shift+Right Ctrl+Shift+Left Search+Shift+Right
      Search+Shift+Left Ctrl+Right Ctrl+Left Search+Right Search+Left
      Search+Ctrl+Right Search+Ctrl+Left Ctrl+Backspace Alt+Backspace
      Alt+Shift+Backspace Ctrl+Shift+V Ctrl+Z Ctrl+Shift+Z`,
  },
  {
    of: "Microsoft Edge, Windows",
    source:
      "https://support.microsoft.com/en-us/microsoft-edge/keyboard-shortcuts-in-microsoft-edge-50d3edab-30d9-c7e4-21ce-37fe2713cfad",
    platforms: ["windows"],
    anywhere: `Ctrl+Shift+B Alt+Shift+B Ctrl+D Ctrl+Shift+D Ctrl+Shift+E
      Alt+D Ctrl+E Alt+E Ctrl+F Alt+F Ctrl+G Ctrl+Shift+G Ctrl+H
      Ctrl+Shift+I Alt+Shift+I Ctrl+J Ctrl+K Ctrl+Shift+K Ctrl+L
      Ctrl+Shift+L Ctrl+M Ctrl+Shift+M Ctrl+N Ctrl+Shift+N Ctrl+O
      Ctrl+Shift+O Ctrl+P Ctrl+Shift+P Ctrl+R Ctrl+Shift+R Ctrl+S Ctrl+T
      Ctrl+Shift+T Alt+Shift+T Ctrl+U Ctrl+Shift+U Ctrl+W Ctrl+Shift+W
      Ctrl+Shift+Y Ctrl+0 Ctrl+1 Ctrl+2 Ctrl+3 Ctrl+4 Ctrl+5 Ctrl+6 Ctrl+7
      Ctrl+8 Ctrl+9 Ctrl+Tab Ctrl+Shift+Tab Ctrl+Plus Ctrl+-
      Ctrl+Shift+Delete Alt+Left Alt+Right Alt+Home Alt+F4 F1 F3 F4 Ctrl+F4
      F5 Shift+F5 F6 Shift+F6 Ctrl+F6 F7 F9 F10 Shift+F10 F11 F12
      Ctrl+PageDown Ctrl+PageUp`,
    text: "Ctrl+Shift+V",
  },
  {
    // From Firefox's own declarations of its keys (browser-sets.inc, and
    // browserSets.ftl for their letters), whose help page refuses to be
    // read by a script, and its developer tools' documented keys, whose
    // letters key-shortcuts.ftl gives: Ctrl and Shift on Windows, Cmd and
    // Option on macOS.
    of: "Firefox, Windows",
    source:
      "https://github.com/mozilla/gecko-dev/blob/master/browser/base/content/browser-sets.inc",
    platforms: ["windows"],
    anywhere: `Ctrl+N Ctrl+Shift+N Ctrl+T Ctrl+Shift+T Ctrl+L Alt+D Ctrl+K
      Ctrl+E Ctrl+J Ctrl+Shift+A Ctrl+O Ctrl+S Ctrl+P Ctrl+W Ctrl+Shift+W
      Ctrl+M Ctrl+Shift+Q Alt+Left Alt+Right Alt+Home F5 Ctrl+F5 Ctrl+R
      Ctrl+Shift+R F11 F9 Ctrl+Shift+] Ctrl+F Ctrl+G Ctrl+Shift+G F3
      Shift+F3 Ctrl+D Ctrl+Shift+D Ctrl+Shift+O Ctrl+B Ctrl+Shift+B Ctrl+H
      Ctrl+Shift+H Ctrl+U Ctrl+I Shift+Escape Ctrl+Shift+Tab Ctrl+1 Ctrl+2
      Ctrl+3 Ctrl+4 Ctrl+5 Ctrl+6 Ctrl+7 Ctrl+8 Ctrl+9 Ctrl+Shift+X
      Ctrl+Shift+P Ctrl+Shift+S Ctrl+Shift+Delete Ctrl+Alt+X Ctrl+Alt+Z
      Ctrl+Shift+I Ctrl+Shift+J Ctrl+Shift+C Ctrl+Shift+K Ctrl+Shift+Z
      Ctrl+Shift+E Ctrl+Shift+M Ctrl+Alt+Shift+I F12 Shift+F7 Shift+F5
      Shift+F9 Shift+F12 Ctrl+Shift+1 Ctrl+Shift+2 Ctrl+Plus Ctrl+-
      Ctrl+0`,
    text: "Ctrl+Z Ctrl+Y",
  },
  {
    of: "Firefox, macOS",
    source:
      "https://github.com/mozilla/gecko-dev/blob/master/browser/base/content/browser-sets.inc",
    platforms: ["macos"],
    anywhere: `Cmd+N Cmd+Shift+N Cmd+T Cmd+Shift+T Cmd+L Cmd+K Cmd+Alt+F
      Cmd+J Cmd+Shift+A Cmd+O Cmd+S Cmd+P Cmd+W Cmd+Shift+W Ctrl+M Cmd+M
      Cmd+Q Cmd+Left Cmd+Right Cmd+[ Cmd+] Alt+Home F5 Cmd+R Cmd+Shift+R
      F11 Ctrl+Cmd+F Cmd+Shift+F Cmd+Alt+R Cmd+Alt+Shift+] Cmd+F Cmd+G
      Cmd+Shift+G F3 Shift+F3 Cmd+E Cmd+D Cmd+Shift+D Cmd+Shift+O Cmd+B
      Cmd+Shift+B Cmd+Shift+H Cmd+Y Cmd+U Cmd+Alt+U Cmd+I Cmd+.
      Shift+Escape Ctrl+Shift+Tab Cmd+1 Cmd+2 Cmd+3 Cmd+4 Cmd+5 Cmd+6 Cmd+7
      Cmd+8 Cmd+9 Cmd+Shift+X Cmd+Shift+P Cmd+Shift+S Cmd+Shift+Delete
      Cmd+Shift+Backspace Ctrl+X Ctrl+Z Ctrl+Shift+3 Ctrl+Shift+4
      Ctrl+Shift+6 Cmd+, Cmd+H Cmd+Alt+H Cmd+Alt+I Cmd+Alt+J Cmd+Alt+C
      Cmd+Alt+K Cmd+Alt+Z Cmd+Alt+E Cmd+Alt+M Cmd+Alt+Shift+I F12 Shift+F7
      Shift+F5 Shift+F9 Shift+F12 Ctrl+Shift+1 Ctrl+Shift+2 Cmd+Plus Cmd+=
      Cmd+- Cmd+0`,
    text: "Cmd+Z Cmd+Shift+Z",
  },
  {
    of: "Safari, macOS",
    source:
      "https://support.apple.com/guide/safari/keyboard-and-other-shortcuts-cpsh003/mac",
    platforms: ["macos"],
    anywhere: `Cmd+F Alt+Tab Ctrl+Tab Ctrl+Shift+Tab Cmd+Shift+\\ Cmd+L Cmd+P
      Cmd+C Cmd+V Cmd+] Cmd+[ Cmd+Shift+] Cmd+Shift+[ Cmd+1 Cmd+2 Cmd+3
      Cmd+4 Cmd+5 Cmd+6 Cmd+7 Cmd+8 Cmd+9 Cmd+W Cmd+Shift+T Cmd+Shift+H
      Cmd+, Cmd+= Cmd+- Cmd+\` Ctrl+Cmd+2 Cmd+Shift+D Cmd+Shift+R
      Ctrl+Cmd+1`,
  },
  {
    of: "macOS",
    source: "https://support.apple.com/en-us/102650",
    platforms: ["macos"],
    anywhere: `Ctrl+Cmd+Space Cmd+Space Alt+Cmd+Space Cmd+Tab Cmd+\`
      Cmd+Shift+\` Cmd+Alt+\` Cmd+X Cmd+C Cmd+V Cmd+A Cmd+F Cmd+G
      Cmd+Shift+G Cmd+H Cmd+Alt+H Cmd+M Cmd+Alt+M Cmd+O Cmd+P Cmd+Q Cmd+S
      Cmd+T Cmd+W Cmd+Alt+W Cmd+Alt+Escape Ctrl+Cmd+F Cmd+, Cmd+Shift+3
      Cmd+Shift+4 Cmd+Shift+5 Ctrl+Cmd+Q Cmd+Shift+Q Cmd+Alt+Shift+Q
      Ctrl+Up Ctrl+Down Ctrl+Left Ctrl+Right Ctrl+Space Ctrl+Alt+Space
      Cmd+Shift+? Cmd+Alt+F5 Ctrl+F2 Ctrl+F3 Ctrl+F4 Ctrl+F5 Ctrl+F6
      Ctrl+Shift+F6 Ctrl+F7 Ctrl+F8 Ctrl+Tab Ctrl+Shift+Tab Cmd+Shift+6
      Cmd+Shift+Space Ctrl+Alt+Cmd+8 Ctrl+Alt+Cmd+, Ctrl+Alt+Cmd+.`,
    text: `Cmd+Z Cmd+Shift+Z Cmd+Up Cmd+Down Cmd+Left Cmd+Right Alt+Left
      Alt+Right Cmd+Shift+Up Cmd+Shift+Down Cmd+Shift+Left Cmd+Shift+Right
      Alt+Shift+Up Alt+Shift+Down Alt+Shift+Left Alt+Shift+Right
      Alt+Backspace Ctrl+H Ctrl+D Ctrl+K Ctrl+Y Ctrl+A Ctrl+E Ctrl+F Ctrl+B
      Ctrl+L Ctrl+P Ctrl+N Ctrl+O Ctrl+T Cmd+B Cmd+I Cmd+U Cmd+K
      Cmd+Shift+: Cmd+; Cmd+{ Cmd+} Cmd+Shift+| Cmd+Alt+T Cmd+Alt+C
      Cmd+Alt+V Cmd+Alt+Shift+V Cmd+Alt+I Cmd+Shift+P Cmd+Shift+S
      Cmd+Shift+- Cmd+Shift+Plus Cmd+=`,
  },
  {
    // The system's own keys, and those of its applications' common
    // editing; those of File Explorer, of the taskbar, of dialogs, and of
    // the command prompt alone are left out.
    of: "Windows",
    source:
      "https://support.microsoft.com/en-us/windows/keyboard-shortcuts-in-windows-dcc61a57-8ff0-cffe-9796-cb9706c75eec",
    platforms: ["windows"],
    anywhere: `Ctrl+A Ctrl+Alt+Delete Ctrl+Alt+Tab Ctrl+Escape Ctrl+F4
      Ctrl+F5 Ctrl+R Ctrl+Shift+Escape Ctrl+Space Ctrl+C Ctrl+Insert Ctrl+V
      Shift+Insert Ctrl+X Ctrl+F Alt+Escape Alt+F4 Alt+Left Alt+Right
      Alt+PageUp Alt+PageDown Alt+PrintScreen Alt+Space Alt+Tab Shift+F10
      F5 F6 F10 F11 Win+A Win+Shift+A Win+Alt+B Win+C Win+Alt+D
      Win+Alt+Down Win+Alt+H Win+Alt+K Win+Alt+Up Win+, Win+Ctrl+C
      Win+Ctrl+Enter Win+Ctrl+F Win+Ctrl+Q Win+Ctrl+Shift+B Win+Ctrl+Space
      Win+Ctrl+V Win+D Win+Down Win+E Win+Escape Win+F Win+/ Win+G Win+H
      Win+Home Win+I Win+J Win+K Win+L Win+Left Win+M Win+- Win+N Win+O
      Win+P Win+Pause Win+. Win+; Win+Plus Win+PrintScreen Win+Q Win+R
      Win+Right Win+S Win+Shift+Down Win+Shift+Enter Win+Shift+Left
      Win+Shift+Right Win+Shift+M Win+Shift+R Win+Shift+S Win+Shift+Space
      Win+Shift+Up Win+Shift+V Win+Space Win+Tab Win+U Win+Up Win+V Win+W
      Win+X Win+Y Win+Z Win+Ctrl+D Win+Ctrl+Left Win+Ctrl+Right
      Win+Ctrl+F4 Win+B Win+T Win+Shift+T Win+1 Win+2 Win+3 Win+4 Win+5
      Win+6 Win+7 Win+8 Win+9 Win+0`,
    text: `Ctrl+Y Ctrl+Z Ctrl+Left Ctrl+Right Ctrl+Up Ctrl+Down Ctrl+Shift+Left
      Ctrl+Shift+Right Ctrl+Shift+Up Ctrl+Shift+Down Ctrl+Shift+V Ctrl+B
      Ctrl+I Ctrl+U Ctrl+H Ctrl+Home Ctrl+End Ctrl+Backspace Ctrl+Delete
      Ctrl+Shift+Home Ctrl+Shift+End Shift+Left Shift+Right Shift+Up
      Shift+Down Shift+Home Shift+End Shift+PageUp Shift+PageDown`,
  },
];

/** A combination of keys as a platform's lists write it, read (parseCombo). */
export interface Combo {
  ctrl: boolean;
  alt: boolean;
  shift: boolean;
  /** Cmd on macOS, Win on Windows, and Search on ChromeOS. */
  meta: boolean;
  /** The key, in lower case, as + or arrowleft. */
  key: string;
}

/** The names of keys that the lists write otherwise than KeyboardEvent.key does, in lower case. */
const aliases: Readonly<Record<string, string>> = {
  esc: "escape",
  left: "arrowleft",
  right: "arrowright",
  up: "arrowup",
  down: "arrowdown",
  pgup: "pageup",
  pgdn: "pagedown",
  del: "delete",
  plus: "+",
  return: "enter",
  space: " ",
};

/** Returns a key in lower case, as KeyboardEvent.key names it. */
export function keyOf(name: string): string {
  const lower = name.toLowerCase();
  return aliases[lower] ?? lower;
}

/** Reads a combination, as Ctrl+Shift+T, and throws for one that it cannot. */
export function parseCombo(text: string): Combo {
  // The key is what follows the last +, which may itself be -, as in
  // Ctrl+-: the + key is written Plus.
  const parts = text.split("+");
  const key = parts.pop();
  if (key === undefined || key === "") {
    throw new Error(`no key in ${text}`);
  }
  const combo: Combo = {
    ctrl: false,
    alt: false,
    shift: false,
    meta: false,
    key: keyOf(key),
  };
  for (const part of parts) {
    switch (part) {
      case "Ctrl":
        combo.ctrl = true;
        break;
      case "Alt":
        combo.alt = true;
        break;
      case "Shift":
        combo.shift = true;
        break;
      case "Cmd":
      case "Win":
      case "Search":
        combo.meta = true;
        break;
      default:
        throw new Error(`unknown modifier ${part} in ${text}`);
    }
  }
  return combo;
}

/** Returns the combinations of a list's part, separated by white space. */
export function combosOf(part: string | undefined): string[] {
  return (part ?? "").split(/\s+/).filter((c) => c !== "");
}
