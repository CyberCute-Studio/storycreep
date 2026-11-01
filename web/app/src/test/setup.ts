import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Vitest's globals are off, so Testing Library cannot unmount the components
// of a test after it on its own.
afterEach(() => {
  cleanup();
});
