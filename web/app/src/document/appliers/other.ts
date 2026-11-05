// The appliers of the clipboard's paste (PRD FR-EDIT-07) and of the
// validation's mutes (FR-VAL-02). A paste's nodes, outputs, and connections
// are the clipboard's, but what their texts, speakers, covers, targets, and
// expressions become depends on what the destination's names resolve to, the
// trash among them, and on what the server compiles, so the applier predicts
// none of them: the store reads the board's content again, which brings them
// all in a few lists, where an unsure prediction of each of a thousand nodes
// and their connections would have the store read each apart. A mute is in
// no entity's body, but changes the revision of the entity whose finding it
// mutes, unless it changes nothing, as a mute with the reason that the
// finding has, which the store then sets back, as the operation's revisions
// do not name it.

import { type Appliers, canonical } from "../apply";
import { boardPart, type Draft, nodeOfOutput, placeOf } from "../doc";
import { renames, settled, withField } from "./node";

type Types = "clipboard.paste" | "validation.mute" | "validation.unmute";

/** Changes the entity of entityId, whose finding a mute changes, as the server changes it: its revision alone. */
function touchOwner(draft: Draft, entityId: string): void {
  const id = canonical(entityId);
  const place = placeOf(id);
  if (place?.of === "output") {
    const node = nodeOfOutput(draft.all("nodes"), id);
    const output = node?.outputs?.find((o) => o.id === id);
    if (node === undefined || output === undefined) {
      return;
    }
    const next = { ...output, updated_at: draft.time };
    const outputs = node.outputs?.map((o) => (o.id === id ? next : o));
    if (draft.putQuietly("nodes", withField(node, "outputs", outputs))) {
      draft.changed(id, settled(draft, next));
    }
    return;
  }
  switch (place?.of === "kind" ? place.kind : undefined) {
    case "nodes": {
      const node = draft.get("nodes", id);
      if (node !== undefined) {
        const next = { ...node, updated_at: draft.time };
        draft.put("nodes", next, settled(draft, next));
      }
      return;
    }
    case "connections": {
      const connection = draft.get("connections", id);
      if (connection !== undefined) {
        const next = { ...connection, updated_at: draft.time };
        draft.put("connections", next, settled(draft, next));
      }
      return;
    }
    case "variables": {
      const variable = draft.get("variables", id);
      if (variable !== undefined) {
        draft.put("variables", { ...variable, updated_at: draft.time });
      }
      return;
    }
    case "boards": {
      const board = draft.get("boards", id);
      if (board !== undefined) {
        draft.put("boards", { ...board, updated_at: draft.time });
      }
      return;
    }
  }
}

export const otherAppliers: Appliers<Types> = {
  "clipboard.paste": (draft, p) => {
    draft.reread(boardPart(canonical(p.board_id)));
    // The paste creates nodes, which node references may name.
    renames(
      draft,
      Object.values(p.ids)
        .map((id) => canonical(id))
        .filter((id) => {
          const place = placeOf(id);
          return place?.of === "kind" && place.kind === "nodes";
        }),
    );
  },
  "validation.mute": (draft, p) => touchOwner(draft, p.entity_id),
  "validation.unmute": (draft, p) => touchOwner(draft, p.entity_id),
};
