// The registration hook of the test-only synthetic version of the published
// game format for the TypeScript tests (PRD FR-DATA-03, plan notes N2 and
// N84): importing it registers the version with play mode's loader
// (registerSchemaVersion), with its JSON Schemas, which the Go tests generate
// from version 1's (README.md), so that the loader reads a snapshot of it.
// Only tests import it, and a test of web/shared fails on any other file
// that does, so that no build of the web app reads the version.

import { registerSchemaVersion } from "../../web/shared/src/flow/versions";
import board from "./published/v2/board.schema.json" with { type: "json" };
import componentTypes from "./published/v2/component_types.schema.json" with {
  type: "json",
};
import components from "./published/v2/components.schema.json" with {
  type: "json",
};
import settings from "./published/v2/settings.schema.json" with {
  type: "json",
};
import manifest from "./published/v2/storycreep.schema.json" with {
  type: "json",
};
import variables from "./published/v2/variables.schema.json" with {
  type: "json",
};

/**
 * The synthetic version's number, one above the newest real version, whose
 * schemas published/v2 holds, and whose fixture fixtures/v2.
 */
export const SYNTHETIC_VERSION = 2;

registerSchemaVersion(SYNTHETIC_VERSION, {
  manifest,
  settings,
  variables,
  componentTypes,
  components,
  board,
});
