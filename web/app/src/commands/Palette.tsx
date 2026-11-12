// The command palette (PRD FR-EDIT-10, plan note N72): a dialog that Ctrl+K,
// Cmd+K on macOS, or the header's Commands opens, of every command that the
// page shown can run, by name, with its keys, which the words typed narrow
// to those whose name has a word that starts as each of them does, whatever
// their case and diacritics. The arrows move through them, and Enter, a
// click, or a tap runs one, once the dialog has closed and the focus is
// back where it was, as if its key had been pressed there. In a project, a
// last entry searches the project for what was typed.

import { foldCase } from "@storycreep/shared/fold";
import { Dialog } from "radix-ui";
import {
  type KeyboardEvent,
  type RefObject,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { DialogFrame } from "../ui/Dialog";
import {
  type CommandId,
  commandName,
  commandOf,
  commandOfKey,
} from "./catalog";
import { KeyCaps } from "./KeyCaps";
import { type CommandRegistry, usePlatform } from "./registry";

/** An entry of the palette: a command, or the search of the project for what was typed. */
type Entry =
  | { kind: "command"; id: CommandId; enabled: boolean }
  | { kind: "search"; query: string };

/** Returns text folded as the palette compares it: by case, as the server folds tags, and without diacritics. */
export function foldWords(text: string): string[] {
  return foldCase(text.normalize("NFD").replace(/\p{M}/gu, ""))
    .split(/[^\p{L}\p{N}]+/u)
    .filter((w) => w !== "");
}

/** Reports whether name has a word that starts as each word of query does. */
export function nameMatches(name: string, query: string): boolean {
  const words = foldWords(name);
  return foldWords(query).every((q) => words.some((w) => w.startsWith(q)));
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
          // The palette has no Dialog.Trigger, which Radix would give the
          // focus back to, as a key opens it: the focus goes back to what
          // had it, or, where that has gone, to the page's content, which
          // the shell gives it (keepFocusOnPage); and then the command
          // runs, which may move it on, as the inspector's Connect to.
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            const back = opener.current;
            if (back?.isConnected === true) {
              back.focus({ preventScroll: true });
            }
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
  const [active, setActive] = useState(0);
  const listId = useId();
  const inputId = useId();
  const entries = useMemo((): Entry[] => {
    const commands: Entry[] = registry
      .available()
      .filter((id) => nameMatches(commandName(t, id), query))
      .map((id) => ({
        kind: "command",
        id,
        enabled: registry.handler(id)?.enabled !== false,
      }));
    const search = registry.handler("project.search");
    return query.trim() !== "" && search !== undefined
      ? [...commands, { kind: "search", query: query.trim() }]
      : commands;
  }, [registry, query, t]);
  const shown = Math.min(active, Math.max(0, entries.length - 1));
  const optionId = (i: number) => `${listId}-${i}`;
  useEffect(() => {
    document
      .getElementById(optionId(shown))
      ?.scrollIntoView?.({ block: "nearest" });
  });
  const choose = (entry: Entry | undefined) => {
    if (entry === undefined || (entry.kind === "command" && !entry.enabled)) {
      return;
    }
    onChoose(entry);
  };
  const keyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (commandOfKey(e, "page", platform) === "palette.open") {
      e.preventDefault();
      onClose();
      return;
    }
    switch (e.key) {
      case "ArrowDown":
        setActive(entries.length === 0 ? 0 : (shown + 1) % entries.length);
        break;
      case "ArrowUp":
        setActive(
          entries.length === 0
            ? 0
            : (shown - 1 + entries.length) % entries.length,
        );
        break;
      case "Enter":
        choose(entries[shown]);
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
        aria-activedescendant={
          entries.length === 0 ? undefined : optionId(shown)
        }
        autoComplete="off"
        spellCheck={false}
        value={query}
        onChange={(e) => {
          setQuery(e.currentTarget.value);
          setActive(0);
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
          // as a combobox's pattern has it, so an option takes none itself.
          // biome-ignore lint/a11y/useFocusableInteractive: as above.
          // biome-ignore lint/a11y/useKeyWithClickEvents: as above.
          <div
            key={entry.kind === "command" ? entry.id : "search"}
            id={optionId(i)}
            role="option"
            aria-selected={i === shown}
            aria-disabled={
              entry.kind === "command" && !entry.enabled ? true : undefined
            }
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
