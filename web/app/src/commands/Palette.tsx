// The command palette (PRD FR-EDIT-10, plan note N72): a dialog that Ctrl+K,
// Cmd+K on macOS, or the header's Commands opens, of every command that the
// page shown can run, by name, with its keys, which the words typed narrow
// to those whose name has a word that starts as each of them does, whatever
// their case and diacritics, and what is typed of no word, as + alone, to
// none of them. The arrows move through them, and Enter, a
// click, or a tap runs one, once the dialog has closed and the focus is
// back where it was, as if its key had been pressed there. In a project, a
// last entry searches the project for what was typed. Whether each can run
// now follows the page while the palette is open, as an undo under way
// ends.

import { Dialog } from "radix-ui";
import {
  type KeyboardEvent,
  type RefObject,
  useEffect,
  useId,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { useTranslation } from "react-i18next";
import { searchText } from "../search/query";
import { DialogFrame, focusBackTo } from "../ui/Dialog";
import { nameMatcher } from "../ui/match";
import {
  type CommandId,
  commandName,
  commandOf,
  commandOfKey,
} from "./catalog";
import { KeyCaps } from "./KeyCaps";
import { isTextField } from "./keys";
import { type CommandRegistry, usePlatform } from "./registry";

/** An entry of the palette: a command, or the search of the project for what was typed, and whether it can run now. */
type Entry =
  | { kind: "command"; id: CommandId; enabled: boolean }
  | { kind: "search"; query: string; enabled: boolean };

/** Returns what tells entry apart from the palette's others: its command's ID, or that it is the search. */
function entryKey(entry: Entry): string {
  return entry.kind === "command" ? entry.id : "search";
}

/**
 * Keeps the repeats of key, held down, from the page until it goes up, or
 * another key goes down, which its repeats stop for: as of the Enter that
 * chose an entry, held down a little long, once the palette has closed and
 * given the focus back to what had it, as the header's Commands, or the
 * toolbar's Delete, or to what the command gave it to, as a dialog's
 * button, which its repeats would else press, as a button's Enter does,
 * opening the palette again, or deleting what was selected.
 */
function swallowRepeats(key: string): void {
  const down = (e: globalThis.KeyboardEvent) => {
    if (e.key !== key || !e.repeat) {
      stop();
      return;
    }
    e.preventDefault();
    e.stopImmediatePropagation();
  };
  const up = (e: globalThis.KeyboardEvent) => {
    if (e.key === key) {
      stop();
    }
  };
  const stop = () => {
    window.removeEventListener("keydown", down, true);
    window.removeEventListener("keyup", up, true);
  };
  window.addEventListener("keydown", down, true);
  window.addEventListener("keyup", up, true);
}

/**
 * The palette, open while open says so, of the commands of registry. It
 * tells onOpenChange that it closes, and gives the focus back to opener,
 * what had it as the palette was asked for, before it runs what was
 * chosen.
 */
export function Palette({
  registry,
  open,
  opener,
  onOpenChange,
}: {
  registry: CommandRegistry;
  open: boolean;
  opener: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  // What the palette was asked to run, which runs once the focus is back.
  const chosen = useRef<Entry | undefined>(undefined);
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      {open && (
        <DialogFrame
          title={t("palette.title")}
          // A press anywhere in the palette but in its field, as on an
          // entry, between two, on its title, or on its edge, leaves the
          // focus in the field, as a combobox's pattern has it, whose keys
          // move through the entries, run one, and close the palette, rather
          // than give it to the dialog itself, as an entry that cannot run
          // now, which closes nothing, would, where those keys would reach
          // none of them: Ctrl+K would be the page's then, which runs
          // nothing while a dialog is open.
          onMouseDown={(event) => {
            if (!isTextField(event.target)) {
              event.preventDefault();
            }
          }}
          // The palette has no Dialog.Trigger, which Radix would give the
          // focus back to, as a key opens it: the focus goes back to what
          // had it, without scrolling, or, where that has gone, to the
          // page's content (focusBackTo); and then the command runs, which
          // may move it on, as the inspector's Connect to.
          onCloseAutoFocus={(event) => {
            focusBackTo(() => opener.current, { preventScroll: true })(event);
            const entry = chosen.current;
            chosen.current = undefined;
            if (entry === undefined) {
              return;
            }
            if (entry.kind === "search") {
              registry.run("project.search", entry.query);
            } else {
              registry.run(entry.id);
            }
          }}
        >
          <PaletteBody
            registry={registry}
            onChoose={(entry) => {
              chosen.current = entry;
              onOpenChange(false);
            }}
            onClose={() => onOpenChange(false)}
          />
        </DialogFrame>
      )}
    </Dialog.Root>
  );
}

function PaletteBody({
  registry,
  onChoose,
  onClose,
}: {
  registry: CommandRegistry;
  onChoose: (entry: Entry) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const platform = usePlatform();
  const [query, setQuery] = useState("");
  // The entry that the arrows moved to, by its command, so that it stays
  // highlighted as the entries before it come and go while the palette is
  // open, as once the board can no longer be changed, rather than its place,
  // which another entry would take, and which Enter would run; none, as at
  // first, and once a letter is typed, for the first entry.
  const [active, setActive] = useState<string | undefined>(undefined);
  const listId = useId();
  const inputId = useId();
  // The entries are read anew as the page's handlers change, as once an
  // undo under way has ended, whose Undo is available again, rather than
  // as they were as the palette opened, or as a letter was typed last.
  useSyncExternalStore(registry.subscribe, registry.revision);
  const matches = nameMatcher(query);
  const commands: Entry[] = registry
    .available()
    .filter((id) => matches(commandName(t, id)))
    .map((id) => ({ kind: "command", id, enabled: registry.can(id) }));
  // The search of what was typed, as the server takes it, of at most 200
  // characters, as a paste may make it longer (searchText), which can run
  // where the search's command can, as the entry of that command says.
  const searched = searchText(query);
  const entries: Entry[] =
    searched !== "" && registry.handler("project.search") !== undefined
      ? [
          ...commands,
          {
            kind: "search",
            query: searched,
            enabled: registry.can("project.search"),
          },
        ]
      : commands;
  // The entry highlighted: the one moved to, or the first where none was;
  // and none where the one moved to is no longer listed, as once the page
  // took its command away, until an arrow moves to another, so that Enter
  // runs only what was highlighted as it was pressed.
  const moved =
    active === undefined
      ? 0
      : entries.findIndex((entry) => entryKey(entry) === active);
  const shown = entries.length === 0 ? -1 : moved;
  const optionId = (i: number) => `${listId}-${i}`;
  // The entry highlighted is scrolled into view as the highlight moves, or
  // as what was typed changes, rather than with each render, as of a change
  // of what the page can run, which would take the list back from where the
  // wheel scrolled it.
  const scrolledTo = useRef<string | undefined>(undefined);
  useEffect(() => {
    const now = `${shown} ${query}`;
    if (now === scrolledTo.current) {
      return;
    }
    scrolledTo.current = now;
    document
      .getElementById(optionId(shown))
      ?.scrollIntoView?.({ block: "nearest" });
  });
  // Chooses entry, and reports whether the palette closes to run it: not
  // for one that cannot run now, nor for the palette's own.
  const choose = (entry: Entry | undefined): boolean => {
    if (entry === undefined || !entry.enabled) {
      return false;
    }
    // The palette's own command, chosen in it, shows it as its key would
    // anywhere else, anew, rather than closing it and opening it again.
    if (entry.kind === "command" && entry.id === "palette.open") {
      setQuery("");
      setActive(undefined);
      return false;
    }
    onChoose(entry);
    return true;
  };
  // Moves the highlight to the next entry, or the one before (by), after
  // the one shown, or, where none is, to the first, or the last.
  const moveBy = (by: 1 | -1) => {
    if (entries.length === 0) {
      setActive(undefined);
      return;
    }
    const from = shown === -1 ? (by === 1 ? -1 : 0) : shown;
    const to = (from + by + entries.length) % entries.length;
    setActive(entryKey(entries[to]));
  };
  const keyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    // A key that an input method takes while it composes, as Enter, which
    // ends what was typed in Japanese or Chinese, and the arrows, which
    // choose among its candidates, is the input method's, rather than one
    // that runs the command chosen, or moves through them: as Safari tells
    // of the Enter that ends it, after the composition has ended, by its
    // keyCode alone, 229, which a key that an input method took has.
    if (e.nativeEvent.isComposing || e.keyCode === 229) {
      return;
    }
    // The palette's key closes it, but not its repeats, as of the key that
    // opened it held down a little long, which would close it at once.
    if (commandOfKey(e, "page", platform) === "palette.open") {
      e.preventDefault();
      if (!e.repeat) {
        onClose();
      }
      return;
    }
    switch (e.key) {
      case "ArrowDown":
        moveBy(1);
        break;
      case "ArrowUp":
        moveBy(-1);
        break;
      case "Enter":
        // The repeats of the Enter that closed the palette, held down, are
        // nothing's, wherever the focus goes (swallowRepeats).
        if (choose(shown === -1 ? undefined : entries[shown])) {
          swallowRepeats(e.key);
        }
        break;
      default:
        return;
    }
    e.preventDefault();
  };
  return (
    <div className="space-y-3">
      <label htmlFor={inputId} className="block font-medium">
        {t("palette.label")}
      </label>
      <input
        id={inputId}
        type="text"
        role="combobox"
        aria-expanded={true}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={shown === -1 ? undefined : optionId(shown)}
        autoComplete="off"
        spellCheck={false}
        value={query}
        onChange={(e) => {
          setQuery(e.currentTarget.value);
          setActive(undefined);
        }}
        onKeyDown={keyDown}
        className="min-h-11 w-full rounded-md border border-border bg-canvas px-3 text-fg"
      />
      <p role="status" className="sr-only">
        {t("palette.count", { count: entries.length })}
      </p>
      <div
        id={listId}
        role="listbox"
        aria-label={t("palette.list")}
        className="flex max-h-[55dvh] flex-col gap-2 overflow-y-auto"
      >
        {entries.map((entry, i) => (
          // The input's keys move through the options and choose one
          // (aria-activedescendant), which keeps the focus in the input,
          // as a combobox's pattern has it, so an option takes none itself,
          // nor gives it to the dialog as it is pressed (the palette's
          // onMouseDown).
          // biome-ignore lint/a11y/useFocusableInteractive: as above.
          // biome-ignore lint/a11y/useKeyWithClickEvents: as above.
          <div
            key={entryKey(entry)}
            id={optionId(i)}
            role="option"
            aria-selected={i === shown}
            aria-disabled={entry.enabled ? undefined : true}
            data-command={entry.kind === "command" ? entry.id : undefined}
            onClick={() => choose(entry)}
            className="flex min-h-11 cursor-pointer items-center justify-between gap-3 rounded px-3 aria-disabled:cursor-not-allowed aria-disabled:text-fg-muted aria-selected:bg-surface-hover"
          >
            {entry.kind === "command" ? (
              <>
                <span className="min-w-0 break-words">
                  {commandName(t, entry.id)}
                  {!entry.enabled && (
                    <span className="text-fg-muted">
                      {" "}
                      {t("palette.unavailable")}
                    </span>
                  )}
                </span>
                <KeyCaps bindings={commandOf(entry.id).keys} first />
              </>
            ) : (
              <span className="min-w-0 break-words">
                {t("palette.search_for", { query: entry.query })}
                {!entry.enabled && (
                  <span className="text-fg-muted">
                    {" "}
                    {t("palette.unavailable")}
                  </span>
                )}
              </span>
            )}
          </div>
        ))}
      </div>
      {entries.length === 0 && (
        <p className="text-fg-muted">{t("palette.none")}</p>
      )}
    </div>
  );
}
