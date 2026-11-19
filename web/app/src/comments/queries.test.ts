import type { CommentBody } from "@storycreep/shared/api";
import { describe, expect, it } from "vitest";
import { testProject } from "../test/fake";
import { threadsOf } from "./queries";

// The threads of a project's comments (plan item 74), as the server lists
// them flat, a page at a time.

/** Returns a comment of the thread of root, the first comment's own for none. */
function comment(id: string, root?: string): CommentBody {
  return {
    id,
    project_id: testProject.id,
    thread_id: root ?? id,
    ...(root === undefined ? { resolved: false } : { parent_id: root }),
    target_type: "node",
    target_id: "nd_1",
    deleted: false,
    created_at: "2026-11-10T10:00:00Z",
    updated_at: "2026-11-10T10:00:00Z",
    rev: 1,
  };
}

describe("threadsOf", () => {
  it("groups the flat list into threads, in its order, though a page cuts a thread's comments, and read again once more, takes each comment once", () => {
    const first = [comment("cmt_9"), comment("cmt_10", "cmt_9")];
    // The next page goes on with the thread, and the next thread, after the
    // first page was read again with a reply more.
    const next = [
      comment("cmt_10", "cmt_9"),
      comment("cmt_11", "cmt_9"),
      comment("cmt_5"),
      comment("cmt_6", "cmt_5"),
    ];
    expect(
      threadsOf([...first, ...next]).map((t) => [
        t.root.id,
        ...t.replies.map((r) => r.id),
      ]),
    ).toEqual([
      ["cmt_9", "cmt_10", "cmt_11"],
      ["cmt_5", "cmt_6"],
    ]);
    // A reply whose first comment the list does not hold starts no thread.
    expect(threadsOf([comment("cmt_2", "cmt_1")])).toEqual([]);
  });
});
