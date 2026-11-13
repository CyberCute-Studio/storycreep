// What runs the app's commands (PRD FR-EDIT-10, plan note N72): each part
// of a page registers what it runs a command with while it is shown
// (useCommandHandlers), as the canvas its zoom, and the palette, the canvas's
// keys, and the keys of the page run the command by its ID through the
// registry, so that a command's key and its entry in the palette run the
// same. A part of the page in the middle of a gesture keeps the commands
// that would interrupt it from running, wherever they are asked from, as
// the canvas in the middle of a drag (hold). The keys of the page act
// wherever the focus is (keysOfPage): not while a dialog is open, whose
// keys are its own, as the palette's, nor in a field that the user types
// in, for a command whose key is the field's own there, as the undo's, nor
// once something of the page has taken the key itself, nor, for a command
// that runs once for a key held down, its repeats; and a key of a command
// that the page has is the page's whether the command can run then or not,
// while a dialog is open too, where it runs nothing, but in a field whose
// own key it is. What the palette says of each command follows the page
// while it is open, as the registry tells it of each change of what the
// page can run (subscribe).

import {
  createContext,
  type ReactNode,
  type RefObject,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { type CommandId, commandIds, commandOf, commandOfKey } from "./catalog";
import { isTextField, type Platform, platformOf } from "./keys";

/**
 * What a part of a page runs a command with: run, which reports whether it
 * did something, as a key of the canvas's that did nothing is left to the
 * page, and whether it can run now, which the palette says of it.
 */
export interface Handler {
  /**
   * Runs the command, with what the palette's search was of, for the
   * project's search, and returns false where it did nothing.
   */
  run: (query?: string) => unknown;
  /**
   * Whether it can run now; it can where this is unset. It is read as the
   * command is asked to run, and as the palette lists it, so a handler of
   * what the page does not render anew for, as a drag of the canvas, says
   * it as a getter.
   */
  enabled?: boolean;
}

/** Reports whether handler can run now: there is one, and it does not say that it cannot. */
export function canRun(handler: Handler | undefined): boolean {
  return handler !== undefined && handler.enabled !== false;
}

/** Runs handler, and reports whether it did something: nothing where there is none, or where it cannot run now. */
export function runHandler(
  handler: Handler | undefined,
  query?: string,
): boolean {
  if (handler === undefined || !canRun(handler)) {
    return false;
  }
  return handler.run(query) !== false;
}

/** The handlers of a part of a page, by command. */
export type Handlers = Partial<Record<CommandId, Handler>>;

/**
 * How deep a part of the page is that registers handlers, of which the
 * deepest's handler of a command runs it, as the search's page's, which
 * gives its own field the focus, before the project's, which opens that
 * page: the app's frame, a project's pages, and a page.
 */
export type Level = "app" | "project" | "page";

const depth: Readonly<Record<Level, number>> = { app: 0, project: 1, page: 2 };

interface Layer {
  depth: number;
  /** In which order it was added, of which the latest of a depth wins. */
  order: number;
  handlers: RefObject<Handlers>;
}

/** The handlers that the shown parts of a page have registered. */
export class CommandRegistry {
  private layers: Layer[] = [];
  private added = 0;
  private holds = new Set<(id: CommandId) => boolean>();
  private listeners = new Set<() => void>();
  private changes = 0;
  /** What the page could run as the listeners were told of it last (changed), or undefined before they were. */
  private said: string | undefined;

  /** Adds the handlers of a part of the page, and returns what takes them away. */
  add(level: Level, handlers: RefObject<Handlers>): () => void {
    const layer = { depth: depth[level], order: this.added++, handlers };
    this.layers.push(layer);
    this.changed();
    return () => {
      this.layers = this.layers.filter((l) => l !== layer);
      this.changed();
    };
  }

  /**
   * Keeps each command that holds says it holds from running, while it
   * says so, wherever it is asked from: by its key, on the part of the page
   * that has the focus or elsewhere, or from the palette, or the header's
   * Commands, which a second finger may tap; as the canvas holds all but
   * those that run in the middle of a drag then, which would change the
   * nodes under the drag, leave the board, or open over it (BoardCanvas's
   * whileDragging). It is read as a command is asked to run, and as the
   * palette lists it (can), and returns what takes it away.
   */
  hold(holds: (id: CommandId) => boolean): () => void {
    this.holds.add(holds);
    return () => {
      this.holds.delete(holds);
    };
  }

  /**
   * Calls listener whenever what the page can run has changed, as once a
   * part of the page rendered anew with an undo that it can run now, and
   * returns what stops it: as the palette renders anew, so that what it
   * says of each command is what the page says of it, rather than what it
   * said as the palette opened (useSyncExternalStore's subscribe).
   */
  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    this.said = undefined;
    return () => {
      this.listeners.delete(listener);
    };
  };

  /** Returns how many times what the page can run has changed (useSyncExternalStore's getSnapshot). */
  readonly revision = (): number => this.changes;

  /**
   * Tells the listeners that the handlers may have changed, as a part of
   * the page that registers them rendered anew (subscribe): those that a
   * listener is told of are the changes of which commands the page has,
   * and of whether each can run, rather than every render of every part
   * of the page that has handlers, as of the canvas, which the palette
   * would read its entries anew for. While none listens, each counts, as
   * a listener that subscribes compares the count with the one that it
   * rendered with.
   */
  changed(): void {
    if (this.listeners.size === 0) {
      this.changes++;
      return;
    }
    const now = this.available()
      .map((id) => (this.can(id) ? id : `-${id}`))
      .join(" ");
    if (now === this.said) {
      return;
    }
    this.said = now;
    this.changes++;
    for (const listener of [...this.listeners]) {
      listener();
    }
  }

  /** Returns the handler that runs the command of id, or undefined while no part of the page has one. */
  handler(id: CommandId): Handler | undefined {
    let best: Layer | undefined;
    for (const layer of this.layers) {
      if (
        layer.handlers.current[id] !== undefined &&
        (best === undefined ||
          layer.depth > best.depth ||
          (layer.depth === best.depth && layer.order > best.order))
      ) {
        best = layer;
      }
    }
    return best?.handlers.current[id];
  }

  /** Returns the commands that the page can run, in the order of the palette. */
  available(): CommandId[] {
    return commandIds.filter((id) => this.handler(id) !== undefined);
  }

  /** Reports whether the command of id can run now (canRun), and nothing holds it (hold). */
  can(id: CommandId): boolean {
    if (!canRun(this.handler(id))) {
      return false;
    }
    for (const holds of this.holds) {
      if (holds(id)) {
        return false;
      }
    }
    return true;
  }

  /** Runs the command of id, and reports whether it did something: nothing where it cannot run now (can). */
  run(id: CommandId, query?: string): boolean {
    return this.can(id) && runHandler(this.handler(id), query);
  }
}

/**
 * Runs the command of id of registry from button, as its click asks, which
 * gives button the focus first, so that the dialog that the command opens,
 * as the palette or the table of the shortcuts, gives it back to button as
 * it closes, as Radix's dialogs do to their Dialog.Trigger: Safari does not
 * focus a button that is clicked or tapped, nor does Firefox on macOS,
 * which leave the focus on the page's body, which the dialog would give
 * the page's content the focus for instead (AppCommands), as the error
 * console's opener says too (ErrorConsole's show). A press of a command
 * that cannot run now runs nothing and leaves the focus where it is, as a
 * second finger's tap in the middle of a drag of the canvas, which holds
 * the command (hold), and keeps the focus that its keys, as Escape, which
 * ends the drag, act with.
 */
export function runFrom(
  registry: CommandRegistry,
  id: CommandId,
  button: HTMLElement,
): void {
  if (!registry.can(id)) {
    return;
  }
  button.focus({ preventScroll: true });
  registry.run(id);
}

const Registry = createContext<CommandRegistry | undefined>(undefined);
const PlatformOf = createContext<Platform>("linux");

/** Returns the page's registry, or undefined outside the app's frame. */
export function useRegistry(): CommandRegistry | undefined {
  return useContext(Registry);
}

/** Returns the platform whose keys the page's shortcuts follow. */
export function usePlatform(): Platform {
  return useContext(PlatformOf);
}

/**
 * Registers handlers, as the part of the page that calls it has them in
 * its latest render, while it is shown, at level, and tells the registry
 * of each render, which may have changed what they can run now, which the
 * registry tells its listeners of where it did, as the palette that is
 * open (CommandRegistry's changed). It is named apart from the project's
 * useCommands, which sends a batch of commands of the project's content
 * (N62).
 */
export function useCommandHandlers(level: Level, handlers: Handlers): void {
  const registry = useContext(Registry);
  const ref = useRef(handlers);
  useLayoutEffect(() => {
    ref.current = handlers;
    registry?.changed();
  });
  useLayoutEffect(
    () => (registry === undefined ? undefined : registry.add(level, ref)),
    [registry, level],
  );
}

/**
 * Returns what takes a key that went down anywhere on the page to the
 * command of the page that it is (FR-EDIT-10): not one that something of
 * the page took itself, as the canvas its clipboard's keys, nor, for a
 * command whose key is a text field's own there, as the undo's, one in
 * such a field. A key of a command that the page has is then the page's
 * whether the command runs or not, rather than the browser's, as Safari's
 * Cmd+Z, which reopens the tab that was closed last: as the undo's while a
 * step is under way, or in the middle of a drag of the canvas (hold), or
 * while a dialog is open, whose keys are its own, as the palette's, which
 * runs nothing, wherever the focus is: in the dialog, or on the page's
 * body, where a dialog leaves it, as a sheet whose text a click gave the
 * focus to none of, or a confirmation whose button that had it is disabled
 * while its request is answered, which Radix's dialogs, which say nothing
 * of being modal (aria-modal), leave it on; and so are the repeats of a key
 * held down of a command that runs once for it (CommandSpec's repeats), as
 * the palette's and the search's, which would open and close them by turns,
 * but for the undo's and the redo's, which go on.
 */
export function keysOfPage(
  registry: CommandRegistry,
  platform: Platform,
): (e: KeyboardEvent) => void {
  return (e) => {
    if (e.defaultPrevented) {
      return;
    }
    const id = commandOfKey(e, "page", platform);
    if (id === undefined) {
      return;
    }
    const target = e.target instanceof Element ? e.target : null;
    if (commandOf(id).outsideText === true && isTextField(target)) {
      return;
    }
    if (registry.handler(id) === undefined) {
      return;
    }
    e.preventDefault();
    const dialogOpen =
      document.querySelector(
        '[role="dialog"], [role="alertdialog"], [aria-modal="true"]',
      ) !== null;
    if (!dialogOpen && (!e.repeat || commandOf(id).repeats === true)) {
      registry.run(id);
    }
  };
}

/** Holds the page's registry, and takes the keys of the page to its commands (keysOfPage). */
export function CommandsProvider({ children }: { children: ReactNode }) {
  const [registry] = useState(() => new CommandRegistry());
  const [platform] = useState(() => platformOf());
  useEffect(() => {
    const listen = keysOfPage(registry, platform);
    document.addEventListener("keydown", listen);
    return () => document.removeEventListener("keydown", listen);
  }, [registry, platform]);
  return (
    <Registry.Provider value={registry}>
      <PlatformOf.Provider value={platform}>{children}</PlatformOf.Provider>
    </Registry.Provider>
  );
}
