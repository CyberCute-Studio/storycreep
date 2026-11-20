// The versions of the published game format that the loader reads (PRD
// §6.4, FR-DATA-03, plan item 77, note N84), each with its JSON Schemas,
// which load checks a snapshot's files against: version 1 alone, whose
// schemas schema/published/v1 holds, which play mode's projection writes;
// and, in a test that imports the registration hook of
// testdata/synthetic-schema, the test-only synthetic version above it
// (registerSchemaVersion), which no other code imports, so that no build
// reads it. A manifest of a version that the registry lacks is the runtime's
// schema_unsupported (SchemaUnsupportedError).

import boardSchema from "../../../../schema/published/v1/board.schema.json" with {
  type: "json",
};
import componentTypesSchema from "../../../../schema/published/v1/component_types.schema.json" with {
  type: "json",
};
import componentsSchema from "../../../../schema/published/v1/components.schema.json" with {
  type: "json",
};
import settingsSchema from "../../../../schema/published/v1/settings.schema.json" with {
  type: "json",
};
import manifestSchema from "../../../../schema/published/v1/storycreep.schema.json" with {
  type: "json",
};
import variablesSchema from "../../../../schema/published/v1/variables.schema.json" with {
  type: "json",
};
import { semantics } from "../expr";
import { ANY_VALUE, type Schema } from "./schema";

/**
 * The JSON Schemas of a version of the published format, of each file of a
 * snapshot that load reads: storycreep.json, project/settings.json,
 * project/variables.json, project/component_types.json,
 * project/components.json, and a board's file.
 */
export interface SchemaDocuments {
  readonly manifest: unknown;
  readonly settings: unknown;
  readonly variables: unknown;
  readonly componentTypes: unknown;
  readonly components: unknown;
  readonly board: unknown;
}

/** The versions from min to max, both included. */
export interface SchemaRange {
  readonly min: number;
  readonly max: number;
}

/**
 * @internal What load checks a version's files against: each file's schema,
 * a board's without its ASTs (withoutASTs), and of the manifest what the Go
 * types check of it (goTypes), which Go's Load reads its astVersion of.
 */
export interface VersionSchemas {
  readonly manifest: Schema;
  readonly manifestTypes: Schema;
  readonly settings: Schema;
  readonly variables: Schema;
  readonly componentTypes: Schema;
  readonly components: Schema;
  readonly board: Schema;
}

// withoutASTs returns the schema of a board's file with the ast of each
// expression, a condition's or a script's, taken as any value (AnyValue), so
// that it reads none of the definitions of an AST: readBoard decodes each
// AST that the schema would have read, and decode refuses exactly what those
// definitions refuse, as the Go test TestDecodeAgreesWithSchema and its twin
// in flow.test.ts check, so that the loader reads each AST once, not twice.
function withoutASTs(board: Schema): Schema {
  type Definition = { readonly properties: Record<string, Schema> };
  const defs = { ...(board as { $defs: Record<string, Definition> }).$defs };
  for (const name of ["Condition", "Script"]) {
    defs[name] = {
      ...defs[name],
      properties: { ...defs[name].properties, ast: { $ref: ANY_VALUE } },
    };
  }
  return { ...(board as object), $defs: defs };
}

// The keywords whose values are schemas, which goTypes reads too: one, a
// list of them, or an object of them; but for propertyNames, which it
// leaves out.
const subschemas = {
  one: new Set(["additionalProperties", "items"]),
  list: new Set(["oneOf"]),
  object: new Set(["$defs", "properties"]),
};

// The keywords of a pattern or a bound, which the Go types do not check.
const patternsAndBounds = new Set([
  "maxItems",
  "maxLength",
  "maximum",
  "minItems",
  "minLength",
  "minimum",
  "pattern",
  "propertyNames",
]);

// goTypes returns the schema s with only what the Go types check of a file
// as published.Decode reads it into them: without a pattern, a pattern of an
// object's keys (propertyNames), or a bound, but for an integer's, which is
// within ±MAX_INT, as Go's Int reads one whatever its schema's minimum and
// maximum. Its types, its constants, its enums, its variants, and the keys
// that it requires are those of s.
function goTypes(s: Schema): Schema {
  if (typeof s !== "object") {
    return s;
  }
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(s)) {
    if (patternsAndBounds.has(k)) {
      continue;
    }
    if (subschemas.one.has(k)) {
      out[k] = goTypes(v as Schema);
    } else if (subschemas.list.has(k)) {
      out[k] = (v as Schema[]).map(goTypes);
    } else if (subschemas.object.has(k)) {
      out[k] = Object.fromEntries(
        Object.entries(v as Record<string, Schema>).map(([name, d]) => [
          name,
          goTypes(d),
        ]),
      );
    } else {
      out[k] = v;
    }
  }
  if (out.type === "integer") {
    out.minimum = -semantics.MAX_INT;
    out.maximum = semantics.MAX_INT;
  }
  return out;
}

// prepared returns what load checks the files of a version of the documents
// docs against.
function prepared(docs: SchemaDocuments): VersionSchemas {
  return {
    manifest: docs.manifest as Schema,
    manifestTypes: goTypes(docs.manifest as Schema),
    settings: docs.settings as Schema,
    variables: docs.variables as Schema,
    componentTypes: docs.componentTypes as Schema,
    components: docs.components as Schema,
    board: withoutASTs(docs.board as Schema),
  };
}

// version1 is what load checks version 1's files against.
const version1 = prepared({
  manifest: manifestSchema,
  settings: settingsSchema,
  variables: variablesSchema,
  componentTypes: componentTypesSchema,
  components: componentsSchema,
  board: boardSchema,
});

// versions are the versions that load reads, by their numbers, from the
// oldest to the newest, each the one after the one before it.
const versions = new Map<number, VersionSchemas>([[1, version1]]);

/** @internal The schema that load checks a board's file of version 1 against. */
export const BOARD_SCHEMA = version1.board;

/**
 * Returns the versions of the published format that load reads, the oldest
 * to the newest: 1 alone, but in a test that registered the synthetic
 * version.
 */
export function supportedSchema(): SchemaRange {
  const numbers = [...versions.keys()];
  return { min: Math.min(...numbers), max: Math.max(...numbers) };
}

/**
 * @internal Returns what load checks the files of the version against, or
 * undefined where it does not read the version.
 */
export function schemasOf(version: number): VersionSchemas | undefined {
  return versions.get(version);
}

/**
 * The test-only registration hook of a version of the published format
 * (plan note N2): adds the version, the one after the newest that load
 * reads, with its JSON Schemas, which load then checks a snapshot of the
 * version against, reading it as it reads every other, so that only a
 * version that differs from the one before in what its schemas take, as the
 * synthetic version does, may be registered. Only
 * testdata/synthetic-schema/register.ts calls it, which only tests import.
 * It throws unless the version is the one after the newest.
 */
export function registerSchemaVersion(
  version: number,
  documents: SchemaDocuments,
): void {
  const newest = supportedSchema().max;
  if (version !== newest + 1) {
    throw new Error(
      `version ${version} of the published format is registered after ${newest}`,
    );
  }
  versions.set(version, prepared(documents));
}
