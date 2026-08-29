import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library only auto-cleans when a global afterEach exists at import
// time; registering it here keeps every suite isolated.
afterEach(() => {
  cleanup();
});
