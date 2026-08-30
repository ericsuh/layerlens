import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { LayerGraph } from "../api/types";
import { GOLDEN_GRAPH } from "../fixtures";
import { renderApp } from "../testing";
import { LayerGraphPanel } from "./LayerGraphPanel";
import { boundsOf, defaultSelection } from "./selection";
import type { SelectionAction } from "./selection";

function renderGraph(graph: LayerGraph = GOLDEN_GRAPH, overrides: Partial<{ l: number; r: number }> = {}) {
  const onSelect = vi.fn<(action: SelectionAction) => void>();
  const selection = { ...defaultSelection(boundsOf(graph)), ...overrides };
  const result = renderApp(
    <LayerGraphPanel graph={graph} selection={selection} onSelect={onSelect} />,
  );
  return { ...result, onSelect, selection };
}

describe("LayerGraphPanel", () => {
  it("renders every trunk layer as a shared card", () => {
    renderGraph();
    for (let count = 1; count <= GOLDEN_GRAPH.trunk.length; count += 1) {
      const card = screen.getByTestId(`layer-card-trunk-${String(count)}`);
      expect(card).toHaveAttribute("role", "radio");
      expect(within(card).getByText("SHARED")).toBeInTheDocument();
    }
  });

  it("attributes branch cards to their own image through separate radiogroups", () => {
    renderGraph();
    const groupA = screen.getByRole("radiogroup", { name: "Image A comparison point" });
    const groupB = screen.getByRole("radiogroup", { name: "Image B comparison point" });
    expect(within(groupA).getAllByRole("radio")).toHaveLength(GOLDEN_GRAPH.leftBranch.length);
    expect(within(groupB).getAllByRole("radio")).toHaveLength(GOLDEN_GRAPH.rightBranch.length);
  });

  it("marks the full stack on each side as selected on entry", () => {
    renderGraph();
    const lastLeft = screen.getByTestId(`layer-card-a-${String(GOLDEN_GRAPH.left.layerCount)}`);
    const lastRight = screen.getByTestId(`layer-card-b-${String(GOLDEN_GRAPH.right.layerCount)}`);
    expect(lastLeft).toHaveAttribute("aria-checked", "true");
    expect(lastRight).toHaveAttribute("aria-checked", "true");
  });

  it("renders the cleaned instruction with its keyword split out", () => {
    renderGraph();
    const card = screen.getByTestId("layer-card-a-6");
    expect(card).toHaveTextContent("COPY");
    expect(card).toHaveTextContent(". .");
    // The card never shows the builder decoration the raw string carries.
    expect(card.textContent).not.toContain("# buildkit");
  });

  it("keeps the raw instruction available behind a popover", async () => {
    const user = userEvent.setup();
    renderGraph();
    const card = screen.getByTestId("layer-card-a-6");
    await user.click(within(card).getByRole("button", { name: "Show the raw instruction" }));
    expect(await screen.findByText("COPY . . # buildkit")).toBeInTheDocument();
  });

  it("shows an empty layer as 0 B with no size bar", () => {
    renderGraph();
    // Trunk layer 5 is `WORKDIR /app`: a real layer with no content bytes.
    const card = screen.getByTestId("layer-card-trunk-5");
    expect(card).toHaveTextContent("0 B · empty");
  });

  it("renders exactly one dotted edge per couldBeShared pair", () => {
    renderGraph();
    expect(screen.getAllByTestId("could-be-shared-edge")).toHaveLength(
      GOLDEN_GRAPH.couldBeShared.length,
    );
  });

  it("puts a ≈ chip on both cards of every could-be-shared pair", () => {
    renderGraph();
    const chips = screen.getAllByRole("button", {
      name: /Could have been shared with a layer in the other image/,
    });
    expect(chips).toHaveLength(GOLDEN_GRAPH.couldBeShared.length * 2);
  });

  it("explains a could-be-shared pair without ever calling it a shared layer", async () => {
    const user = userEvent.setup();
    renderGraph();
    const chip = screen.getAllByRole("button", {
      name: /Could have been shared with a layer in the other image/,
    })[0];
    expect(chip).toBeDefined();
    await user.click(chip as HTMLElement);

    const popover = await screen.findByRole("dialog", { name: "Could-be-shared layers" });
    const text = popover.textContent ?? "";
    expect(text).toContain("could have been shared");
    // The honesty rule (RESEARCH Q9): this is equivalence under the build-cache
    // rule, never a cache hit that actually happened.
    expect(text).toContain("build-cache rule");
    expect(text).toContain("built and stored separately");
    expect(text).not.toMatch(/\bshared layer\b/i);
    expect(text).not.toMatch(/\bcache hit\b/i);
    expect(text).not.toMatch(/\bwere shared\b/i);
    expect(text).not.toMatch(/\bare shared\b/i);
  });

  it("reports a trunk click as setting both comparison points", async () => {
    const user = userEvent.setup();
    const { onSelect } = renderGraph();
    await user.click(screen.getByTestId("layer-card-trunk-3"));
    expect(onSelect).toHaveBeenCalledWith({ type: "select-trunk", count: 3 });
  });

  it("reports a branch click as moving only that side", async () => {
    const user = userEvent.setup();
    const { onSelect } = renderGraph();
    await user.click(screen.getByTestId("layer-card-b-7"));
    expect(onSelect).toHaveBeenCalledWith({ type: "select-right", count: 7 });
  });

  it("selects with Space, and moves focus with the arrow keys instead", async () => {
    const user = userEvent.setup();
    const { onSelect } = renderGraph();
    const card = screen.getByTestId("layer-card-a-6");
    card.focus();

    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(screen.getByTestId("layer-card-a-7"));
    expect(onSelect).not.toHaveBeenCalled();

    await user.keyboard("{ArrowRight}");
    expect(document.activeElement).toBe(screen.getByTestId("layer-card-b-7"));

    await user.keyboard(" ");
    expect(onSelect).toHaveBeenCalledWith({ type: "select-right", count: 7 });
  });

  it("walks from a branch back up into the trunk with ArrowUp", async () => {
    const user = userEvent.setup();
    renderGraph();
    screen.getByTestId("layer-card-a-6").focus();
    await user.keyboard("{ArrowUp}");
    expect(document.activeElement).toBe(screen.getByTestId("layer-card-trunk-5"));
  });

  it("jumps to the ends of a column with Home and End", async () => {
    const user = userEvent.setup();
    renderGraph();
    screen.getByTestId("layer-card-trunk-3").focus();
    await user.keyboard("{End}");
    expect(document.activeElement).toBe(screen.getByTestId("layer-card-trunk-5"));
    await user.keyboard("{Home}");
    expect(document.activeElement).toBe(screen.getByTestId("layer-card-trunk-1"));
  });

  it("shows the selection as a chip per side", () => {
    renderGraph(GOLDEN_GRAPH, { l: 6, r: 8 });
    expect(screen.getByTestId("selection-chip-a")).toHaveTextContent("A @ layer 6");
    expect(screen.getByTestId("selection-chip-b")).toHaveTextContent("B @ layer 8");
  });

  describe("degenerate cases", () => {
    it("explains a fork at the root when there is no shared trunk", () => {
      const graph: LayerGraph = {
        ...GOLDEN_GRAPH,
        trunkLength: 0,
        trunk: [],
        leftBranch: GOLDEN_GRAPH.trunk.slice(0, 2),
        rightBranch: GOLDEN_GRAPH.rightBranch,
        couldBeShared: [],
      };
      renderGraph(graph);
      expect(screen.getByTestId("no-shared-layers")).toHaveTextContent("No shared layers");
      expect(screen.queryByTestId("layer-card-trunk-1")).toBeNull();
    });

    it("draws a strict prefix as one straight stack with a note", () => {
      const graph: LayerGraph = {
        ...GOLDEN_GRAPH,
        leftBranch: [],
        couldBeShared: [],
      };
      renderGraph(graph);
      expect(screen.getByTestId("prefix-note")).toHaveTextContent("fully contained in");
      // No fork means no elbow into either column.
      expect(screen.queryByTestId("fork-a")).toBeNull();
      expect(screen.queryByTestId("fork-b")).toBeNull();
    });

    it("notes identical images and shows only trunk cards", () => {
      const graph: LayerGraph = {
        ...GOLDEN_GRAPH,
        leftBranch: [],
        rightBranch: [],
        couldBeShared: [],
      };
      renderGraph(graph);
      expect(screen.getByTestId("identical-note")).toHaveTextContent("identical at every layer");
      expect(screen.queryByTestId("could-be-shared-edge")).toBeNull();
    });

    it("renders a long branch without dropping cards", () => {
      const long = Array.from({ length: 24 }, (_, i) => {
        const base = GOLDEN_GRAPH.rightBranch[0];
        if (base === undefined) {
          throw new Error("fixture has no right branch");
        }
        return { ...base, index: 5 + i, diffId: `sha256:${String(i).padStart(64, "0")}` };
      });
      renderGraph({ ...GOLDEN_GRAPH, rightBranch: long, couldBeShared: [] });
      const groupB = screen.getByRole("radiogroup", { name: "Image B comparison point" });
      expect(within(groupB).getAllByRole("radio")).toHaveLength(24);
    });
  });
});
