import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Vitest's globals are off, so Testing Library cannot unmount the components
// of a test after it on its own. What a page kept in the browser's storage,
// as the workspace that it last showed, is the test's alone.
afterEach(() => {
  cleanup();
  // The tests of the lint rules run in Node, which has no window.
  if (typeof window !== "undefined") {
    window.localStorage.clear();
    window.sessionStorage.clear();
  }
});
