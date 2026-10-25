import { expect, test } from "vitest";
import ast from "../../../../schema/published/v1/ast.schema.json" with {
  type: "json",
};
import board from "../../../../schema/published/v1/board.schema.json" with {
  type: "json",
};
import componentTypes from "../../../../schema/published/v1/component_types.schema.json" with {
  type: "json",
};
import components from "../../../../schema/published/v1/components.schema.json" with {
  type: "json",
};
import settings from "../../../../schema/published/v1/settings.schema.json" with {
  type: "json",
};
import manifest from "../../../../schema/published/v1/storycreep.schema.json" with {
  type: "json",
};
import variables from "../../../../schema/published/v1/variables.schema.json" with {
  type: "json",
};
import saveState from "../../../../schema/save-state/v1/save-state.schema.json" with {
  type: "json",
};
import {
  astExamples,
  boardExamples,
  componentsExamples,
  componentTypesExamples,
  saveStateExamples,
  settingsExamples,
  storycreepExamples,
  variablesExamples,
} from "./testdata/examples.gen";

// The examples module holds each document's examples typed by the types that
// are generated from the documents, which the TypeScript compiler checks them
// against (pnpm typecheck), as the web app's code is checked against them.
// This test checks that they are the documents' own examples, so that the
// types take what the schemas describe.
// Verifies: FR-DATA-04/AC2
test("the generated types take the examples of the JSON Schemas", () => {
  const pairs: [{ examples?: unknown[] }, unknown[]][] = [
    [ast, astExamples],
    [board, boardExamples],
    [componentTypes, componentTypesExamples],
    [components, componentsExamples],
    [settings, settingsExamples],
    [manifest, storycreepExamples],
    [variables, variablesExamples],
    [saveState, saveStateExamples],
  ];
  for (const [schema, examples] of pairs) {
    expect(examples.length).toBeGreaterThan(0);
    expect(examples).toEqual(schema.examples);
  }
});
