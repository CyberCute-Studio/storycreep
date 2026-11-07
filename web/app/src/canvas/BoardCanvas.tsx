// A board's canvas (PRD FR-EDIT-01, FR-EDIT-11, ADR-013): the board's nodes
// and connections, as the project's document holds them, drawn by PixiJS,
// with a toolbar that switches what one finger's drag does, pan or select
// (NFR-TOUCH-03), zooms, fits the content, zooms to the selection, and leads
// to the board's settings under the canvas, and a minimap. The canvas
// suppresses the browser's own gestures over it, as pull-to-refresh and
// overscroll (NFR-TOUCH-08), and its menu, and the toolbar's controls are of
// the size of a finger (NFR-TOUCH-01).

import { formatNumber } from "@storycreep/shared/i18n";
import {
  lazy,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  Suspense,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { useStore } from "zustand";
import { createStore, type StoreApi } from "zustand/vanilla";
import { boardPart } from "../document/doc";
import { useProjectDocument } from "../document/react";
import { followInPage } from "../shell/focus";
import { Button, buttonClass } from "../ui/Button";
import { mediaQueryList, useMediaQuery } from "../ui/media";
import {
  type BenchOptions,
  CanvasController,
  renderedMark,
} from "./controller";
import type { Engine, RendererKind } from "./engine";
import type { Mode, PointerInput } from "./gestures";
import {
  type LabelContext,
  type LabelSources,
  labelContext,
  labelsOf,
  namesChanged,
} from "./labels";
import { Minimap } from "./minimap";
import { readTheme } from "./theme";

const Bench = lazy(() => import("./bench"));

/** What the board's page asks of its canvas. */
export interface CanvasOptions {
  /** The renderer to use, whatever the browser's would be, as the benchmark and the tablet's checklist compare them. */
  renderer?: RendererKind;
  bench?: BenchOptions;
}

const reducedMotionQuery = "(prefers-reduced-motion: reduce)";
const darkQuery = "(prefers-color-scheme: dark)";

/** The zoom that the toolbar shows, as the canvas tells it, in a store of its own. */
type ZoomShown = StoreApi<number>;

/**
 * The toolbar's button of the zoom, which shows it and shows 100% again: it
 * alone follows the zoom, which a pinch changes in nearly every frame, so
 * that the rest of the canvas's page is not rendered again with it.
 */
function ZoomReset({
  zoom,
  onClick,
}: {
  zoom: ZoomShown;
  onClick: () => void;
}) {
  const { t, i18n } = useTranslation();
  const value = useStore(zoom);
  const percent = formatNumber(i18n.language, value, {
    style: "percent",
    maximumFractionDigits: 0,
  });
  return (
    <Button
      aria-label={t("canvas.zoom_reset", { zoom: percent })}
      onClick={onClick}
      className="min-w-20 tabular-nums"
    >
      {percent}
    </Button>
  );
}

/**
 * Returns the size of the canvas's element, in CSS pixels, which the
 * renderer's canvas, the rubber band, and the points of the pointers are
 * laid out in: its whole box, as its edge is a ring drawn outside it rather
 * than a border within it.
 */
function sizeOf(host: HTMLElement): { width: number; height: number } {
  const r = host.getBoundingClientRect();
  return { width: r.width, height: r.height };
}

/** Returns the input of a pointer's event, at its point of the element at rect. */
function pointerInput(e: PointerEvent, rect: DOMRect): PointerInput {
  return {
    id: e.pointerId,
    x: e.clientX - rect.left,
    y: e.clientY - rect.top,
    kind:
      e.pointerType === "touch"
        ? "touch"
        : e.pointerType === "pen"
          ? "pen"
          : "mouse",
    button: e.button,
    buttons: e.buttons,
    shift: e.shiftKey,
    additive: e.ctrlKey || e.metaKey,
  };
}

/** The canvas of a board of a project. */
export function BoardCanvas({
  projectId,
  boardId,
  boardName,
  settingsId,
  options = {},
}: {
  projectId: string;
  boardId: string;
  boardName: string;
  /** The ID of the heading of the board's settings, under the canvas, which the toolbar links to, whose element takes the focus. */
  settingsId?: string;
  options?: CanvasOptions;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const helpId = useId();
  const hostRef = useRef<HTMLDivElement>(null);
  const surfaceRef = useRef<HTMLDivElement>(null);
  const bandRef = useRef<HTMLDivElement>(null);
  const minimapRef = useRef<HTMLCanvasElement>(null);
  /** The pointer whose press went down on the minimap, which its drag follows. */
  const minimapPress = useRef<number>(undefined);
  const [mode, setMode] = useState<Mode>("pan");
  const [selected, setSelected] = useState(0);
  const [zoom] = useState<ZoomShown>(() => createStore<number>(() => 1));
  const [content, setContent] = useState<"loading" | "ready" | "failed">(
    "loading",
  );
  const [attempt, setAttempt] = useState(0);
  const [engine, setEngine] = useState<"starting" | "running" | "unsupported">(
    "starting",
  );
  // What the fetch of the renderer's chunk failed with, which the page
  // throws to its route's error (RouteError), as it does a chunk of its own.
  const [engineFailure, setEngineFailure] = useState<{ error: unknown }>();
  const [empty, setEmpty] = useState(false);
  const [showMinimap, setShowMinimap] = useState(true);
  const [height, setHeight] = useState<number>();
  // Whether the system asks for reduced motion (NFR-A11Y-07), as it changes.
  const reducedMotion = useMediaQuery(reducedMotionQuery, false);
  const [controller] = useState(
    () =>
      new CanvasController(boardId, {
        selection: (ids) => setSelected(ids.size),
        zoom: (z) => zoom.setState(z, true),
        rendered: () => {
          try {
            performance.clearMarks(renderedMark);
            performance.mark(renderedMark, { detail: { boardId } });
          } catch {
            // A browser without marks' details has no harness to read them.
          }
        },
      }),
  );

  // The canvas takes the window's height below where it starts, as a
  // tablet's screen is the board's, as the window's size changes, and as
  // what is above it changes its height, as the toolbar that wraps once it
  // says how many nodes are selected, or an alert at the page's top: the
  // page, taller than the window once the canvas takes the rest of it,
  // changes its height with it.
  useLayoutEffect(() => {
    const host = hostRef.current;
    if (host === null) {
      return;
    }
    const measure = () => {
      const top = host.getBoundingClientRect().top + window.scrollY;
      setHeight(Math.max(320, window.innerHeight - top - 16));
    };
    measure();
    window.addEventListener("resize", measure);
    const page =
      typeof ResizeObserver === "function"
        ? new ResizeObserver(measure)
        : undefined;
    page?.observe(document.body);
    return () => {
      window.removeEventListener("resize", measure);
      page?.disconnect();
    };
  }, []);

  // The elements, their size, the theme, and the browser's events.
  useLayoutEffect(() => {
    const host = hostRef.current;
    const band = bandRef.current;
    if (host === null || band === null) {
      return;
    }
    controller.attach({ host, band });
    const theme = () => controller.setTheme(readTheme(host));
    theme();
    const dark = mediaQueryList(darkQuery);
    dark?.addEventListener("change", theme);
    const resize = () => {
      controller.setSize(sizeOf(host), window.devicePixelRatio || 1);
    };
    resize();
    const observer =
      typeof ResizeObserver === "function"
        ? new ResizeObserver(resize)
        : undefined;
    observer?.observe(host);
    // The screen's pixels to a CSS pixel, which change without the canvas's
    // size, as the window moves to a screen of another density: a query of
    // the density as it is tells once it is another, and is made anew for
    // the one after.
    let density: MediaQueryList | undefined;
    const densityChanged = () => {
      resize();
      watchDensity();
    };
    const watchDensity = () => {
      density?.removeEventListener("change", densityChanged);
      density =
        typeof window.matchMedia === "function"
          ? window.matchMedia(
              `(resolution: ${window.devicePixelRatio || 1}dppx)`,
            )
          : undefined;
      density?.addEventListener("change", densityChanged);
    };
    watchDensity();
    // A pointer that the canvas follows is the host's, whose capture it
    // takes as the pointer goes down, and which the controller gives back
    // as soon as the gestures no longer follow it (GestureHost's release).
    // Of a pointer that it holds, the host hears that it left only once the
    // browser has lost it, as a pen that leaves the range where the screen
    // sees it hover (Gestures' leave).
    const pointer =
      (type: "down" | "move" | "leave" | "up" | "cancel") =>
      (e: PointerEvent) => {
        if (
          type === "down" &&
          e.target !== host &&
          !surfaceRef.current?.contains(e.target as Node)
        ) {
          return;
        }
        // A pointer that the canvas does not follow, as a mouse that moves
        // over it with no button down, reads nothing of the page's layout.
        if (type !== "down" && !controller.gestures.tracks(e.pointerId)) {
          return;
        }
        const followed = controller.pointer(
          type,
          pointerInput(e, host.getBoundingClientRect()),
        );
        if (type === "down" && followed) {
          host.focus({ preventScroll: true });
          host.setPointerCapture?.(e.pointerId);
          e.preventDefault();
        }
      };
    const down = pointer("down");
    const move = pointer("move");
    const leave = pointer("leave");
    const up = pointer("up");
    const cancel = pointer("cancel");
    // The host's own capture, once it lost it, rather than that of an
    // element in it, as the minimap's, or of a finger on the renderer's
    // canvas, which the browser gives it and the host takes, whose loss
    // bubbles up to it.
    const lost = (e: PointerEvent) => {
      if (e.target === host) {
        cancel(e);
      }
    };
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = host.getBoundingClientRect();
      controller.gestures.wheel({
        x: e.clientX - r.left,
        y: e.clientY - r.top,
        dx: e.deltaX,
        dy: e.deltaY,
        deltaMode: e.deltaMode,
        zoom: e.ctrlKey || e.metaKey,
        shift: e.shiftKey,
      });
    };
    // Safari's pinch of a trackpad, which comes as a gesture's scale.
    let scale = 1;
    const gesture = (e: Event) => {
      e.preventDefault();
      const g = e as Event & {
        scale?: number;
        clientX?: number;
        clientY?: number;
      };
      const r = host.getBoundingClientRect();
      if (e.type === "gesturestart") {
        scale = 1;
      }
      const next = g.scale ?? 1;
      controller.gestures.scale(
        {
          x: (g.clientX ?? r.left + r.width / 2) - r.left,
          y: (g.clientY ?? r.top + r.height / 2) - r.top,
        },
        scale,
        next,
      );
      scale = next;
    };
    // A touch on the canvas, or on its minimap, is the canvas's alone:
    // cancelling it keeps the browser from taking it for a scroll or a
    // fling of its own, which a tap right after, as of the toolbar's
    // button, would only stop, rather than press, and from zooming the page
    // with a double tap (NFR-TOUCH-08). A button over the canvas, as the
    // one that reads its content again, keeps its taps. The browser's menu
    // does not open over the canvas either, as a right click does nothing
    // to the board: a Ctrl+click on a Mac, which the canvas takes as a
    // click that adds a node to the selection, or takes it out, would else
    // open it, and the menu would take the click's release.
    const own = (e: Event) => {
      if (!(e.target instanceof Element && e.target.closest("button"))) {
        e.preventDefault();
      }
    };
    const blur = () => {
      controller.gestures.cancelAll();
      controller.gestures.setSpace(false);
    };
    host.addEventListener("touchstart", own, { passive: false });
    host.addEventListener("touchmove", own, { passive: false });
    host.addEventListener("contextmenu", own);
    host.addEventListener("pointerdown", down);
    host.addEventListener("pointermove", move);
    host.addEventListener("pointerleave", leave);
    host.addEventListener("pointerup", up);
    host.addEventListener("pointercancel", cancel);
    host.addEventListener("lostpointercapture", lost);
    host.addEventListener("wheel", wheel, { passive: false });
    for (const type of ["gesturestart", "gesturechange", "gestureend"]) {
      host.addEventListener(type, gesture);
    }
    window.addEventListener("blur", blur);
    return () => {
      controller.stop();
      controller.attach(undefined);
      dark?.removeEventListener("change", theme);
      observer?.disconnect();
      density?.removeEventListener("change", densityChanged);
      host.removeEventListener("touchstart", own);
      host.removeEventListener("touchmove", own);
      host.removeEventListener("contextmenu", own);
      host.removeEventListener("pointerdown", down);
      host.removeEventListener("pointermove", move);
      host.removeEventListener("pointerleave", leave);
      host.removeEventListener("pointerup", up);
      host.removeEventListener("pointercancel", cancel);
      host.removeEventListener("lostpointercapture", lost);
      host.removeEventListener("wheel", wheel);
      for (const type of ["gesturestart", "gesturechange", "gestureend"]) {
        host.removeEventListener(type, gesture);
      }
      window.removeEventListener("blur", blur);
    };
  }, [controller]);

  // The canvas's size once its height is set, at once, rather than once the
  // observer tells of it, after the next frame: that frame places the canvas
  // on the board's content where it has been read already, as of a board
  // that the page showed before, which it fits to the canvas's height, not
  // to the height of none that the canvas had before, at 10%.
  useLayoutEffect(() => {
    const host = hostRef.current;
    if (host !== null && height !== undefined) {
      controller.setSize(sizeOf(host), window.devicePixelRatio || 1);
    }
  }, [controller, height]);

  // The renderer, on a canvas element of its own in the surface, as a
  // renderer that ends may leave its canvas's context lost. PixiJS, with
  // the renderer's module, is fetched only as the canvas starts it, in a
  // chunk of its own, so that the board's page, its toolbar, and the read
  // of its content, which the renderer draws once it has started, do not
  // wait for the browser to fetch and parse it first. A chunk that cannot
  // be fetched, as once the server has a version of the app other than the
  // page's, is the page's error, as a chunk of the page's own is, rather
  // than a browser that cannot draw.
  useEffect(() => {
    const surface = surfaceRef.current;
    const host = hostRef.current;
    if (surface === null || host === null) {
      return;
    }
    let ended = false;
    let started: Engine | undefined;
    setEngine("starting");
    const fontFamily = getComputedStyle(host).fontFamily || "sans-serif";
    import("./engine").then(
      ({ createEngine }) =>
        createEngine(surface, {
          preference: options.renderer,
          forced: options.renderer !== undefined,
          fontFamily,
        }).then(
          (e) => {
            if (ended) {
              e.destroy();
              return;
            }
            started = e;
            controller.setEngine(e);
            setEngine("running");
          },
          () => {
            if (!ended) {
              setEngine("unsupported");
            }
          },
        ),
      (error: unknown) => {
        if (!ended) {
          setEngineFailure({ error });
        }
      },
    );
    return () => {
      ended = true;
      controller.setEngine(undefined);
      started?.destroy();
    };
  }, [controller, options.renderer]);

  // The minimap, while it is shown.
  useEffect(() => {
    const canvas = minimapRef.current;
    controller.setMinimap(
      showMinimap && canvas !== null ? new Minimap(canvas) : undefined,
    );
  }, [controller, showMinimap]);

  // The board's content, from the project's document, as it changes.
  useEffect(() => {
    let names: LabelSources | undefined;
    const sync = () => {
      const view = doc.store.getState().view;
      // Whether a name that cards show changed (labels.ts).
      const changed = names !== undefined && namesChanged(names, view);
      names = view;
      controller.sync(view, changed);
      setEmpty(controller.scene.nodes.size === 0);
    };
    // The keys that labels names, each a literal there, are the catalog's,
    // as extraction reads them. What labels read of the document is made
    // once for each state of it that they read, rather than for each card.
    let context: { view: LabelSources; c: LabelContext } | undefined;
    controller.labels = (item) => {
      const view = doc.store.getState().view;
      if (context?.view !== view) {
        context = { view, c: labelContext(view, t) };
      }
      return labelsOf(item.node, context.c);
    };
    // The messages of t, which is another as the locale changes.
    controller.relabel();
    sync();
    return doc.store.subscribe(sync);
  }, [controller, doc, t]);

  // The board's nodes and connections, read once, and then the names that
  // its cards show, of its speakers and of the variables that its texts
  // show, which the board's first frame does not wait for.
  // biome-ignore lint/correctness/useExhaustiveDependencies: attempt reads the board's content again, as the user asks for it.
  useEffect(() => {
    let ended = false;
    setContent("loading");
    doc.load(boardPart(boardId)).then(
      () => {
        if (ended) {
          return;
        }
        setContent("ready");
        controller.setReady();
        void doc.load("components").catch(() => {});
        void doc.load("variables").catch(() => {});
      },
      () => {
        if (!ended) {
          setContent("failed");
        }
      },
    );
    return () => {
      ended = true;
    };
  }, [controller, doc, boardId, attempt]);

  useEffect(() => {
    controller.reducedMotion = reducedMotion;
  }, [controller, reducedMotion]);

  useEffect(() => {
    controller.setMode(mode);
  }, [controller, mode]);

  const keyDown = (e: ReactKeyboardEvent) => {
    if (e.target !== e.currentTarget) {
      return;
    }
    if (e.key === " " && !e.ctrlKey && !e.metaKey && !e.altKey) {
      controller.gestures.setSpace(true);
      e.preventDefault();
      return;
    }
    if (controller.key(e)) {
      e.preventDefault();
    }
  };
  const keyUp = (e: ReactKeyboardEvent) => {
    if (e.key === " ") {
      controller.gestures.setSpace(false);
    }
  };
  // Space let go of once the focus has left the canvas goes up elsewhere,
  // so the canvas lets go of it as the focus leaves, rather than pan on
  // every drag of the select mode after.
  const blur = () => controller.gestures.setSpace(false);
  const minimapRelease = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    if (minimapPress.current === e.pointerId) {
      minimapPress.current = undefined;
    }
  };
  const minimapPointer = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    // The primary button's alone, as a finger's touch or a pen's tip, as on
    // the canvas: a right click, the middle button, or a pen's barrel
    // button or its eraser, does nothing to the board here either, nor a
    // move without the primary button down, which ends the minimap's press
    // whose release it did not hear of, nor one of a press that went down
    // elsewhere, as a mouse's on the page's text beside the canvas, that
    // crosses the minimap, which follows only its own.
    if (e.type === "pointerdown") {
      if (e.button !== 0) {
        return;
      }
      minimapPress.current = e.pointerId;
      e.currentTarget.setPointerCapture?.(e.pointerId);
    } else if ((e.buttons & 1) === 0) {
      minimapRelease(e);
      return;
    } else if (minimapPress.current !== e.pointerId) {
      return;
    }
    e.stopPropagation();
    e.preventDefault();
    const r = e.currentTarget.getBoundingClientRect();
    controller.centerOnMinimap({ x: e.clientX - r.left, y: e.clientY - r.top });
  };

  if (engineFailure !== undefined) {
    throw engineFailure.error;
  }

  const overlay =
    engine === "unsupported"
      ? { role: "alert" as const, text: t("canvas.unsupported") }
      : content === "failed"
        ? { role: "alert" as const, text: t("canvas.load_failed") }
        : content === "loading"
          ? { role: "status" as const, text: t("canvas.loading") }
          : empty
            ? { role: "status" as const, text: t("canvas.empty") }
            : undefined;

  return (
    <div className="space-y-2">
      <fieldset className="flex min-w-0 flex-wrap items-center gap-2">
        <legend className="sr-only">{t("canvas.controls")}</legend>
        <fieldset className="flex min-w-0 gap-1">
          <legend className="sr-only">{t("canvas.mode")}</legend>
          <Button
            aria-pressed={mode === "pan"}
            onClick={() => setMode("pan")}
            className={mode === "pan" ? "outline-2 outline-accent" : ""}
          >
            {t("canvas.mode_pan")}
          </Button>
          <Button
            aria-pressed={mode === "select"}
            onClick={() => setMode("select")}
            className={mode === "select" ? "outline-2 outline-accent" : ""}
          >
            {t("canvas.mode_select")}
          </Button>
        </fieldset>
        <div className="flex gap-1">
          <Button
            aria-label={t("canvas.zoom_out")}
            onClick={() => controller.zoomStep(-1)}
          >
            −
          </Button>
          <ZoomReset zoom={zoom} onClick={() => controller.zoomTo(1)} />
          <Button
            aria-label={t("canvas.zoom_in")}
            onClick={() => controller.zoomStep(1)}
          >
            +
          </Button>
        </div>
        <Button onClick={() => controller.fit()}>{t("canvas.fit")}</Button>
        <Button
          disabled={selected === 0}
          onClick={() => controller.zoomToSelection()}
        >
          {t("canvas.zoom_selection")}
        </Button>
        <Button
          aria-pressed={showMinimap}
          onClick={() => setShowMinimap((shown) => !shown)}
        >
          {t("canvas.minimap")}
        </Button>
        {settingsId !== undefined && (
          // The board's settings, under the canvas, which neither a swipe
          // nor the wheel scrolls the page to over the canvas, which keeps
          // the browser's gestures off over it (NFR-TOUCH-08), and whose
          // wheel pans the board (N66): the link brings their heading into
          // view and gives it the focus, so that a finger, a mouse, and the
          // keys reach them in one action.
          <a
            href={`#${settingsId}`}
            onClick={(e) => followInPage(e, settingsId)}
            className={buttonClass()}
          >
            {t("canvas.settings")}
          </a>
        )}
        <p className="text-sm text-fg-muted" aria-live="polite">
          {selected > 0 && t("canvas.selected", { count: selected })}
        </p>
      </fieldset>
      <div
        ref={hostRef}
        role="application"
        aria-roledescription={t("canvas.roledescription")}
        aria-label={t("canvas.label", { board: boardName })}
        aria-describedby={helpId}
        // biome-ignore lint/a11y/noNoninteractiveTabindex: the canvas takes the keys that pan and zoom it (NFR-A11Y-04).
        tabIndex={0}
        onKeyDown={keyDown}
        onKeyUp={keyUp}
        onBlur={blur}
        style={{ height }}
        className="relative touch-none overflow-hidden overscroll-none rounded-md bg-canvas ring-1 ring-border select-none focus-visible:outline-3 focus-visible:outline-offset-2 focus-visible:outline-focus"
      >
        <div ref={surfaceRef} className="absolute inset-0" />
        <div
          ref={bandRef}
          hidden
          aria-hidden="true"
          className="pointer-events-none absolute top-0 left-0 border-2 border-accent bg-accent/10"
        />
        {overlay !== undefined && (
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-4">
            {/* The message lets a press on it through to the canvas under
                it, so that a drag that starts on it, as on an empty board,
                pans as one beside it does, as the wheel over it does; its
                button alone, which reads the content again, takes its own. */}
            <p
              role={overlay.role}
              className="max-w-md rounded-md border border-border bg-surface px-4 py-3 text-center"
            >
              {overlay.text}
              {content === "failed" && engine !== "unsupported" && (
                <>
                  {" "}
                  <Button
                    onClick={() => setAttempt((n) => n + 1)}
                    className="pointer-events-auto"
                  >
                    {t("canvas.retry")}
                  </Button>
                </>
              )}
            </p>
          </div>
        )}
        {showMinimap && (
          // The minimap is a pointer's shortcut: the keys pan and fit the
          // board as it does (NFR-A11Y-04), so it is hidden from the
          // accessibility tree, as the canvas's pixels are.
          <div
            aria-hidden="true"
            className="absolute right-3 bottom-3 h-32 w-48 overflow-hidden rounded-md border border-border bg-surface shadow-md"
          >
            <canvas
              ref={minimapRef}
              onPointerDown={minimapPointer}
              onPointerMove={minimapPointer}
              onPointerUp={minimapRelease}
              onPointerCancel={minimapRelease}
              onLostPointerCapture={minimapRelease}
              className="block size-full cursor-pointer"
            />
          </div>
        )}
      </div>
      <p id={helpId} className="text-sm text-fg-muted">
        {t("canvas.help")}
      </p>
      {options.bench !== undefined && (
        <Suspense>
          <Bench handle={controller.handle} options={options.bench} />
        </Suspense>
      )}
    </div>
  );
}
