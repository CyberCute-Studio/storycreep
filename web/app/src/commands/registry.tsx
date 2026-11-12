// What runs the app's commands (PRD FR-EDIT-10, plan note N72): each part
// of a page registers what it runs a command with while it is shown
// (useCommands), as the canvas its zoom, and the palette, the canvas's
// keys, and the keys of the page run the command by its ID through the
// registry, so that a command's key and its entry in the palette run the
// same. The keys of the page act wherever the focus is (keysOfPage): not in
// a dialog, whose keys are its own, as the palette's, nor in a field that
// the user types in, for a command whose key is the field's own there, as
// the undo's, nor once something of the page has taken the key itself.

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
 * did something, as a key that did nothing is left to the browser, and
 * whether it can run now, which the palette says of it.
 */
export interface Handler {
  /**
   * Runs the command, with what the palette's search was of, for the
   * project's search, and returns false where it did nothing.
   */
  run: (query?: string) => unknown;
  /** Whether it can run now; it can where this is unset. */
  enabled?: boolean;
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

  /** Adds the handlers of a part of the page, and returns what takes them away. */
  add(level: Level, handlers: RefObject<Handlers>): () => void {
    const layer = { depth: depth[level], order: this.added++, handlers };
    this.layers.push(layer);
    return () => {
      this.layers = this.layers.filter((l) => l !== layer);
    };
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

  /** Runs the command of id, and reports whether it did something: nothing where it cannot run now. */
  run(id: CommandId, query?: string): boolean {
    const handler = this.handler(id);
    if (handler === undefined || handler.enabled === false) {
      return false;
    }
    return handler.run(query) !== false;
  }
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
 * its latest render, while it is shown, at level.
 */
export function useCommands(level: Level, handlers: Handlers): void {
  const registry = useContext(Registry);
  const ref = useRef(handlers);
  useLayoutEffect(() => {
    ref.current = handlers;
  });
  useLayoutEffect(
    () => (registry === undefined ? undefined : registry.add(level, ref)),
    [registry, level],
  );
}

/**
 * Returns what takes a key that went down anywhere on the page to the
 * command of the page that it is (FR-EDIT-10): not one that something of
 * the page took itself, as the canvas its clipboard's keys, nor one in a
 * dialog, whose keys are its own, as the palette's and a sheet's, nor, for
 * a command whose key is a text field's own there, as the undo's, one in
 * such a field, nor the repeats of the palette's and the search's key held
 * down, which would open and close them by turns.
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
    if (target?.closest('[role="dialog"], [role="alertdialog"]') != null) {
      return;
    }
    if (commandOf(id).outsideText === true && isTextField(target)) {
      return;
    }
    if (e.repeat && (id === "palette.open" || id === "project.search")) {
      e.preventDefault();
      return;
    }
    if (registry.run(id)) {
      e.preventDefault();
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
