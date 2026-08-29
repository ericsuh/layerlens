import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "./App";

describe("App", () => {
  it("renders the application title", () => {
    render(<App />);
    expect(screen.getByTestId("app-title").textContent).toBe("layerlens");
  });

  it("humanizes sizes through the shared formatter", () => {
    render(<App />);
    expect(screen.getByTestId("sample-size").textContent).toBe("14.3 MiB");
  });

  it("renders one swatch per semantic diff state", () => {
    render(<App />);
    const labels = screen.getAllByRole("listitem").map((el) => el.textContent);
    expect(labels).toEqual(["shared", "added", "removed", "modified", "image A", "image B"]);
  });
});
