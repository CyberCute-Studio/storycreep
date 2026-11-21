import type { Diff, EntityChange } from "@storycreep/shared/api";
import { describe, expect, it } from "vitest";
import { refused } from "../test/fake";
import { releaseFiles, zipOf } from "../test/release";
import { readRelease } from "./content";
import {
  buildPoll,
  comparedPair,
  draft,
  majorChanges,
  majorOf,
  nextMajor,
  releaseQuery,
  versionPattern,
} from "./queries";
import { readZip } from "./zip";

// What the pages of a project's releases read of a release's zip, and what
// of a diff suggests a major version (PRD FR-SNAP-02, §7.3, plan note N88).

describe("the poll of a build", () => {
  const next = (status: string, error: unknown) =>
    (
      releaseQuery("prj", "snp").refetchInterval as unknown as (query: {
        state: { data: { status: string }; error: unknown };
      }) => number | false
    )({ state: { data: { status }, error } });

  it("reads a building snapshot again, also after a failure of its own", () => {
    expect(next("building", null)).toBe(buildPoll);
    expect(next("building", new Error("no answer"))).toBe(buildPoll);
    expect(next("building", refused(500, { code: "internal" }))).toBe(
      buildPoll,
    );
    expect(next("ready", null)).toBe(false);
  });

  it("ends at a refusal, and waits out the rate limit", () => {
    expect(next("building", refused(404, { code: "not_found" }))).toBe(false);
    expect(
      next(
        "building",
        refused(429, { code: "rate_limited" }, { "Retry-After": "30" }),
      ),
    ).toBe(30_000);
    expect(next("building", refused(429, { code: "rate_limited" }))).toBe(
      30_000,
    );
  });
});

describe("readZip", () => {
  it("reads each member, deflated or stored, byte for byte", async () => {
    const files = releaseFiles();
    const zip = await zipOf(files, new Set(["project/variables.json"]));
    const read = await readZip(zip);
    expect([...read.keys()]).toEqual([...files.keys()]);
    for (const [path, bytes] of files) {
      expect([...(read.get(path) ?? [])]).toEqual([...bytes]);
    }
  });

  it("reads a zip within a larger buffer", async () => {
    const zip = await zipOf(releaseFiles());
    const padded = new Uint8Array(zip.length + 8);
    padded.set(zip, 8);
    const read = await readZip(padded.subarray(8));
    expect(read.size).toBe(releaseFiles().size);
  });

  it("refuses what is no zip", async () => {
    await expect(readZip(new Uint8Array(4))).rejects.toThrow(/directory/);
    await expect(readZip(new Uint8Array(64))).rejects.toThrow(/directory/);
  });

  it("refuses a member of another method", async () => {
    const files = new Map([
      ["a.json", new TextEncoder().encode("{}") as Uint8Array<ArrayBuffer>],
    ]);
    const zip = await zipOf(files, new Set(["a.json"]));
    // The method of the only entry of the central directory.
    const directory = new DataView(zip.buffer).getUint32(zip.length - 6, true);
    new DataView(zip.buffer).setUint16(directory + 10, 12, true);
    await expect(readZip(zip)).rejects.toThrow(/method 12/);
  });
});

describe("readRelease", () => {
  it("orders a board's nodes from its entry, by its ports and choices", () => {
    const { boards } = readRelease(releaseFiles());
    const harbour = boards[0];
    expect(harbour.reached.map((n) => n.title)).toEqual([
      "Greet",
      "Reply",
      "Gate",
      "To the tower",
    ]);
    expect(harbour.unreached.map((n) => n.title)).toEqual(["Lost"]);
    expect(harbour.leaving.get("nd_a")?.map((c) => c.id)).toEqual([
      "cn_ask",
      "cn_leave",
    ]);
  });

  it("finds a jumper's target on another board", () => {
    const { nodes } = readRelease(releaseFiles());
    expect(nodes.get("nd_t")?.board.name).toBe("Tower");
  });

  it("refuses files that lack one", () => {
    const files = releaseFiles();
    files.delete("project/variables.json");
    expect(() => readRelease(files)).toThrow(/project\/variables\.json/);
  });
});

describe("versions", () => {
  it("takes the versions that the server takes", () => {
    for (const version of ["0.1.0", "1.4.0", "999999999.0.1"]) {
      expect(versionPattern.test(version)).toBe(true);
    }
    for (const version of ["1.4", "01.2.3", "1.2.3-rc", "1000000000.0.0", ""]) {
      expect(versionPattern.test(version)).toBe(false);
    }
  });

  it("bumps the major version, up to the greatest", () => {
    expect(nextMajor("1.3.2")).toBe("2.0.0");
    expect(nextMajor("999999999.0.0")).toBeUndefined();
    expect(nextMajor("nope")).toBeUndefined();
    expect(majorOf("12.0.1")).toBe(12);
  });
});

/** A diff of the summary's removed count and the variables' changes, of which counted are counted. */
function diffOf(
  removed: number,
  variables: EntityChange[],
  counted = variables.length,
): Diff {
  const none = { added: 0, cosmetic: 0, modified: 0, moved: 0, removed: 0 };
  return {
    boards: [],
    from: { kind: "snapshot", contentHash: "" },
    to: { kind: "draft", contentHash: "" },
    schemaVersion: 1,
    truncated: counted > variables.length,
    project: {
      attributes: [],
      componentTypes: [],
      components: [],
      fields: [],
      omitted: counted - variables.length,
      variables,
    },
    summary: {
      added: 0,
      cosmetic: 0,
      modified: counted,
      moved: 0,
      removed,
      kinds: {
        attribute: none,
        board: none,
        branch_output: none,
        component: none,
        component_type: none,
        connection: none,
        node: none,
        project: none,
        variable: { ...none, modified: counted },
      },
    },
  };
}

const change = (field: string): EntityChange => ({
  id: "var_a",
  kind: "variable",
  change: "modified",
  fields: [{ field, before: "int", after: "string" }],
});

describe("comparedPair", () => {
  // Newest first.
  const ready = [{ id: "c" }, { id: "b" }, { id: "a" }];

  it("picks the newest two, or the only release and the draft", () => {
    expect(comparedPair(ready, undefined, undefined)).toEqual({
      from: "b",
      to: "c",
    });
    expect(comparedPair([{ id: "a" }], undefined, undefined)).toEqual({
      from: "a",
      to: draft,
    });
    expect(comparedPair([], undefined, undefined).from).toBeUndefined();
  });

  it("keeps both sides that the address names", () => {
    expect(comparedPair(ready, "a", "c")).toEqual({ from: "a", to: "c" });
  });

  it("compares a from alone into the release after it, or the newest into the draft", () => {
    expect(comparedPair(ready, "a", undefined).to).toBe("b");
    expect(comparedPair(ready, "c", undefined).to).toBe(draft);
  });

  it("compares a to alone from the release before it, and the draft from the newest", () => {
    expect(comparedPair(ready, undefined, "c").from).toBe("b");
    expect(comparedPair(ready, undefined, draft).from).toBe("c");
  });

  it("compares the oldest, named as to alone, into the draft", () => {
    expect(comparedPair(ready, undefined, "a")).toEqual({
      from: "a",
      to: draft,
    });
  });
});

describe("majorChanges", () => {
  it("counts what was removed", () => {
    expect(majorChanges(diffOf(3, []))).toEqual({
      removed: 3,
      retyped: 0,
      unsure: false,
    });
  });

  it("counts a variable given another type, and no other change of one", () => {
    expect(majorChanges(diffOf(0, [change("type")])).retyped).toBe(1);
    expect(majorChanges(diffOf(0, [change("key")])).retyped).toBe(0);
  });

  it("is unsure of a diff that left out changes of variables", () => {
    expect(majorChanges(diffOf(0, [change("key")], 5)).unsure).toBe(true);
  });
});
