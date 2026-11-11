// What the canvas places over its nodes for their connections (PRD
// FR-EDIT-03, FR-EDIT-05, FR-EDIT-09, plan note N69), outside its element,
// as it places the resize handle (ResizeHandle.tsx): the link handles, one
// on each port that connections leave of the one selected node, and one on
// each end of the connection selected, whose press is the canvas's, which
// follows its pointer by its gestures, as a press of the resize handle is,
// and whose drag draws a connection from the port, or takes the
// connection's end, to the node under the pointer; what a connection
// dropped on the empty board offers, to create a node there and connect it
// (FR-EDIT-03 AC3); and the link of the jumper selected to its target. A
// link handle is explicit, a square of the size of a fingertip, 44 CSS
// pixels, on a device with a touch screen, and of 24 on one without
// (FR-EDIT-09), and is shown where its port has room for it. The inspector
// connects, reconnects, and reorders without a drag (WCAG 2.5.7), so the
// handles are hidden from assistive technology.

import type { Node } from "@storycreep/shared/api";
import { Link as RouterLink } from "@tanstack/react-router";
import { Popover } from "radix-ui";
import {
  type PointerEvent,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
} from "react";
import { useTranslation } from "react-i18next";
import { Button, buttonClass } from "../ui/Button";
import { useMediaQuery } from "../ui/media";
import type { CanvasController, PortOf, ScreenRectOf } from "./controller";
import type { Link } from "./gestures";
import { entryPoint, outputOffset, portPoint, portsOf } from "./layout";
import type { Point } from "./viewport";

/** The query of a device with a touch screen, whose link handles are of a fingertip's size (FR-EDIT-09). */
const touchQuery = "(any-pointer: coarse)";

/** A link handle's side, in CSS pixels, on a device with a touch screen, and on one without. */
export const linkHandleSides = { touch: 44, fine: 24 } as const;

/** Where a link handle is on the board, and how much room it has there, in units of the board, along its node's edge. */
interface HandlePlace {
  point: Point;
  room: number;
}

/**
 * Returns where the handle of port of the node of id is on the board of
 * controller: on the port, with the room of the port's row, of a branch of
 * several outputs, or of the node's height; none once the node, or its
 * port, has gone.
 */
export function portPlace(
  controller: CanvasController,
  from: PortOf,
): HandlePlace | undefined {
  const item = controller.scene.nodes.get(from.node);
  if (item === undefined) {
    return undefined;
  }
  const ports = portsOf(item.node);
  if (!ports.includes(from.port)) {
    return undefined;
  }
  const height = item.rect.height;
  return {
    point: portPoint(item.node, item.rect, from.port),
    room:
      ports.length > 1
        ? outputOffset(1, ports.length, height) -
          outputOffset(0, ports.length, height)
        : height,
  };
}

/**
 * Returns where the handle of an end of the connection of id is on the
 * board of controller: where the connection leaves its node (from), or
 * where it enters its node (to); none once the connection has gone.
 */
export function endPlace(
  controller: CanvasController,
  id: string,
  end: "from" | "to",
): HandlePlace | undefined {
  const item = controller.scene.connections.get(id);
  if (item === undefined) {
    return undefined;
  }
  const c = item.connection;
  if (end === "from") {
    const from = controller.scene.nodes.get(c.from_node_id);
    return from === undefined
      ? undefined
      : {
          point: portPoint(from.node, from.rect, c.from_port),
          room: Number.POSITIVE_INFINITY,
        };
  }
  const to = controller.scene.nodes.rect(c.to_node_id);
  return to === undefined
    ? undefined
    : { point: entryPoint(to), room: Number.POSITIVE_INFINITY };
}

/**
 * A link handle, of what link holds, at place, which the canvas reads in
 * each frame that it draws, so that the handle follows its port as the
 * canvas pans, zooms, or moves its node; it is hidden where it has no place,
 * where the canvas draws its place's room smaller than its side, as the
 * rows of a branch's outputs at a low zoom, whose handles would cover each
 * other, and while the selection moves.
 */
export function LinkHandle({
  link,
  controller,
  place,
  onPress,
}: {
  link: Link;
  controller: CanvasController;
  place: () => HandlePlace | undefined;
  /** Takes a press of the handle, which the canvas follows as its own (CanvasController's down). */
  onPress: (e: PointerEvent<HTMLDivElement>, link: Link) => void;
}) {
  const handle = useRef<HTMLDivElement>(null);
  const touch = useMediaQuery(touchQuery, false);
  const side = touch ? linkHandleSides.touch : linkHandleSides.fine;
  // The page's latest place, which the canvas's frames read.
  const placeNow = useRef(place);
  useLayoutEffect(() => {
    placeNow.current = place;
  });
  // What the handle holds, the same object while it holds the same.
  const kind = link.kind;
  const node = link.kind === "new" ? link.node : undefined;
  const port = link.kind === "new" ? link.port : undefined;
  const connection = link.kind === "new" ? undefined : link.connection;
  const held = useMemo<Link>(
    () =>
      kind === "new"
        ? { kind, node: node as string, port: port as string }
        : { kind, connection: connection as string },
    [kind, node, port, connection],
  );

  useEffect(
    () =>
      controller.follow((_: ScreenRectOf) => {
        const el = handle.current;
        if (el === null) {
          return;
        }
        const at = placeNow.current();
        el.hidden =
          at === undefined ||
          controller.moves() ||
          at.room * controller.viewport().zoom < side;
        if (at !== undefined) {
          const p = controller.screenPoint(at.point);
          el.style.transform = `translate(${p.x - side / 2}px, ${
            p.y - side / 2
          }px)`;
        }
      }),
    [controller, side],
  );

  // A handle that goes, as once the selection changes, ends its press, or
  // its drag, as cancelled, as the page takes it away (Gestures' linkGone).
  useLayoutEffect(
    () => () => controller.gestures.linkGone(held),
    [controller, held],
  );

  return (
    <div
      ref={handle}
      aria-hidden="true"
      hidden
      // The wheel, the pinch, and the browser's menu over the handle are the
      // canvas's, as over the canvas (BoardCanvas's layer over it).
      data-canvas=""
      data-link={kind}
      data-port={port}
      onPointerDown={(e) => onPress(e, held)}
      style={{ width: side, height: side }}
      className="pointer-events-auto absolute top-0 left-0 flex cursor-crosshair touch-none items-center justify-center"
    >
      <span className="block size-4 rounded-full border-2 border-accent bg-canvas" />
    </div>
  );
}

/**
 * What a connection dropped on the empty board offers (FR-EDIT-03 AC3): to
 * create a node there, an element, a branch, or a jumper, and connect it,
 * in a box at the point of the board where it was dropped, which follows
 * that point as the canvas moves. Nothing is created until a type is
 * chosen: Cancel, Escape, and a press outside the box leave nothing behind.
 */
export function ConnectOffer({
  controller,
  at,
  onChoose,
  onCancel,
  returnFocus,
}: {
  controller: CanvasController;
  /** The point of the board where the connection was dropped. */
  at: Point;
  onChoose: (type: Node["type"]) => void;
  onCancel: () => void;
  /** Returns what gets the focus once the box has gone, the canvas. */
  returnFocus: () => HTMLElement | null;
}) {
  const { t } = useTranslation();
  const titleId = useId();
  const anchor = useRef<HTMLDivElement>(null);
  useEffect(
    () =>
      controller.follow(() => {
        const el = anchor.current;
        if (el !== null) {
          const p = controller.screenPoint(at);
          el.style.transform = `translate(${p.x}px, ${p.y}px)`;
        }
      }),
    [controller, at],
  );
  const types: { type: Node["type"]; label: string }[] = [
    { type: "element", label: t("canvas.add_element") },
    { type: "branch", label: t("canvas.add_branch") },
    { type: "jumper", label: t("canvas.add_jumper") },
  ];
  return (
    <Popover.Root
      open
      onOpenChange={(open) => {
        if (!open) {
          onCancel();
        }
      }}
    >
      <Popover.Anchor asChild>
        <div ref={anchor} className="absolute top-0 left-0 size-px" />
      </Popover.Anchor>
      <Popover.Portal>
        <Popover.Content
          aria-labelledby={titleId}
          side="right"
          align="start"
          sideOffset={8}
          collisionPadding={8}
          // The box follows its point of the board in each frame, as the
          // wheel pans the canvas under it.
          updatePositionStrategy="always"
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            returnFocus()?.focus({ preventScroll: true });
          }}
          className="z-10 w-64 space-y-2 rounded-md border border-border bg-canvas p-3 text-fg shadow-lg"
        >
          <h2 id={titleId} className="font-semibold">
            {t("canvas.connect_new")}
          </h2>
          <div className="flex flex-col gap-2">
            {types.map(({ type, label }) => (
              <Button key={type} onClick={() => onChoose(type)}>
                {label}
              </Button>
            ))}
            <Button onClick={onCancel}>{t("canvas.connect_cancel")}</Button>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

/**
 * The link of the jumper of id, selected on the canvas, to its target, the
 * node of nodeId on the board of boardId (FR-EDIT-05): under the jumper's
 * card, where the canvas places it in each frame, of the size of a finger,
 * so that one tap, or one click, opens the target on its board's canvas.
 */
export function TargetLink({
  id,
  controller,
  projectId,
  boardId,
  nodeId,
}: {
  id: string;
  controller: CanvasController;
  projectId: string;
  boardId: string;
  nodeId: string;
}) {
  const { t } = useTranslation();
  const box = useRef<HTMLDivElement>(null);
  useEffect(
    () =>
      controller.follow((rectOf: ScreenRectOf) => {
        const el = box.current;
        if (el === null) {
          return;
        }
        const r = rectOf(id);
        el.hidden = r === undefined || controller.moves();
        if (r !== undefined) {
          el.style.transform = `translate(${r.x}px, ${r.y + r.height + 8}px)`;
        }
      }),
    [controller, id],
  );
  return (
    <div ref={box} hidden className="pointer-events-auto absolute top-0 left-0">
      <RouterLink
        to="/projects/$projectId/boards/$boardId"
        params={{ projectId, boardId }}
        search={{ node: nodeId }}
        className={buttonClass()}
      >
        {t("canvas.go_to_target")}
      </RouterLink>
    </div>
  );
}
