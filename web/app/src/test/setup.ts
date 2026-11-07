import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// jsdom draws no canvas, and says so on each call of getContext: a canvas
// has no context here, as in a browser without one, so that a board's
// canvas says that it cannot draw, quietly.
if (typeof HTMLCanvasElement !== "undefined") {
  HTMLCanvasElement.prototype.getContext = (() =>
    null) as typeof HTMLCanvasElement.prototype.getContext;
}

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
