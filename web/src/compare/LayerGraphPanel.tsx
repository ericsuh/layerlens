import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent } from "react";

import type { GraphLayer, LayerGraph } from "../api/types";
import { ImageBadge, ImageRefText, primaryRef } from "../components/identity";
import { Popover } from "../components/ui/popover";
import { CouldBeSharedExplanation, LayerCard } from "./LayerCard";
import type { CardColumn } from "./LayerCard";
import { EdgeOverlay } from "./EdgeOverlay";
import type { OverlayEdge } from "./EdgeOverlay";
import type { Box } from "./geometry";
import { couldBeSharedEdge, selectionRuleBox } from "./geometry";
import { boundsOf, selectionLabel } from "./selection";
import type { LayerSelection, SelectionAction } from "./selection";

/** Which structural case the graph falls into (DESIGN §5.2 "Degenerate cases"). */
export type GraphShape = "identical" | "prefix-left" | "prefix-right" | "fork";

export function graphShape(graph: LayerGraph): GraphShape {
  const leftEmpty = graph.leftBranch.length === 0;
  const rightEmpty = graph.rightBranch.length === 0;
  if (leftEmpty && rightEmpty) {
    return "identical";
  }
  if (leftEmpty) {
    // Left is a strict prefix of right: right extends it.
    return "prefix-right";
  }
  if (rightEmpty) {
    return "prefix-left";
  }
  return "fork";
}

interface Measurements {
  width: number;
  height: number;
  trunk: Box[];
  left: Box[];
  right: Box[];
}

const EMPTY_MEASUREMENTS: Measurements = { width: 0, height: 0, trunk: [], left: [], right: [] };

type FocusKey = `${CardColumn}:${number}`;

export interface LayerGraphPanelProps {
  graph: LayerGraph;
  selection: LayerSelection;
  onSelect: (action: SelectionAction) => void;
}

export function LayerGraphPanel({ graph, selection, onSelect }: LayerGraphPanelProps) {
  const bounds = useMemo(() => boundsOf(graph), [graph]);
  const shape = graphShape(graph);
  const trunkLength = graph.trunk.length;

  const containerRef = useRef<HTMLDivElement | null>(null);
  const noSharedStripRef = useRef<HTMLParagraphElement | null>(null);
  const cardRefs = useRef(new Map<FocusKey, HTMLDivElement>());
  const [measured, setMeasured] = useState<Measurements>(EMPTY_MEASUREMENTS);
  const [hoveredEdge, setHoveredEdge] = useState<number | null>(null);

  // Roving tabindex: one tabbable card per radiogroup, so Tab lands on the
  // group's current answer rather than walking every layer (DESIGN §7).
  const [tabIndexes, setTabIndexes] = useState<Record<CardColumn, number>>({
    trunk: 0,
    a: 0,
    b: 0,
  });

  const columns = useMemo<Record<CardColumn, GraphLayer[]>>(
    () => ({ trunk: graph.trunk, a: graph.leftBranch, b: graph.rightBranch }),
    [graph],
  );

  /**
   * Measures every card relative to the diagram box. Branch columns wrap and
   * scroll independently, so the SVG can only stay attached to the cards if it
   * is told where they actually are.
   */
  const measure = useCallback(() => {
    const container = containerRef.current;
    if (container === null) {
      return;
    }
    const base = container.getBoundingClientRect();
    const boxesFor = (column: CardColumn, length: number): Box[] => {
      const result: Box[] = [];
      for (let i = 0; i < length; i += 1) {
        const node = cardRefs.current.get(`${column}:${i}`);
        if (node === undefined) {
          continue;
        }
        const rect = node.getBoundingClientRect();
        result.push({
          x: rect.left - base.left,
          y: rect.top - base.top,
          w: rect.width,
          h: rect.height,
        });
      }
      return result;
    };
    // With no shared trunk the explanatory strip stands in for the last trunk
    // card, so the fork elbows still leave from something the user can see
    // (DESIGN §5.2, "No shared layers").
    const strip = noSharedStripRef.current;
    const stripBox: Box[] =
      columns.trunk.length === 0 && strip !== null
        ? [
            {
              x: strip.getBoundingClientRect().left - base.left,
              y: strip.getBoundingClientRect().top - base.top,
              w: strip.getBoundingClientRect().width,
              h: strip.getBoundingClientRect().height,
            },
          ]
        : [];
    const next: Measurements = {
      width: base.width,
      height: container.scrollHeight,
      trunk: columns.trunk.length === 0 ? stripBox : boxesFor("trunk", columns.trunk.length),
      left: boxesFor("a", columns.a.length),
      right: boxesFor("b", columns.b.length),
    };
    setMeasured((previous) =>
      JSON.stringify(previous) === JSON.stringify(next) ? previous : next,
    );
  }, [columns]);

  useLayoutEffect(() => {
    measure();
  }, [measure, selection, shape]);

  useEffect(() => {
    const container = containerRef.current;
    if (container === null || typeof ResizeObserver === "undefined") {
      return;
    }
    const observer = new ResizeObserver(() => {
      measure();
    });
    observer.observe(container);
    return () => {
      observer.disconnect();
    };
  }, [measure]);

  // Where each side's comparison point sits, as a column plus an index inside
  // it. Layer counts are 1-based; 0 means "before the first layer".
  const positionOf = useCallback(
    (count: number, branch: "a" | "b"): { column: CardColumn; index: number } | null => {
      if (count <= 0) {
        return null;
      }
      if (count <= trunkLength) {
        return { column: "trunk", index: count - 1 };
      }
      return { column: branch, index: count - 1 - trunkLength };
    },
    [trunkLength],
  );

  const positionA = positionOf(selection.l, "a");
  const positionB = positionOf(selection.r, "b");

  const isSelected = useCallback(
    (column: CardColumn, index: number): boolean =>
      (positionA?.column === column && positionA.index === index) ||
      (positionB?.column === column && positionB.index === index),
    [positionA, positionB],
  );

  /** Could-be-shared partners, resolved to card indexes on both branches. */
  const edges = useMemo(() => {
    return graph.couldBeShared.map((edge, i) => ({
      id: i,
      leftIndex: edge.leftIndex - trunkLength,
      rightIndex: edge.rightIndex - trunkLength,
      diffIdEqual: edge.diffIdEqual,
    }));
  }, [graph.couldBeShared, trunkLength]);

  const edgeForCard = useCallback(
    (column: CardColumn, index: number) => {
      const found = edges.find((edge) =>
        column === "a" ? edge.leftIndex === index : column === "b" && edge.rightIndex === index,
      );
      if (found === undefined) {
        return undefined;
      }
      return {
        id: found.id,
        diffIdEqual: found.diffIdEqual,
        highlighted: hoveredEdge === found.id,
      };
    },
    [edges, hoveredEdge],
  );

  const focusCard = useCallback((column: CardColumn, index: number) => {
    const node = cardRefs.current.get(`${column}:${index}`);
    if (node === undefined) {
      return;
    }
    setTabIndexes((previous) => ({ ...previous, [column]: index }));
    node.focus();
  }, []);

  /** DESIGN §7's keyboard map for the composite layer widget. */
  const handleKeyDown = useCallback(
    (column: CardColumn, index: number, count: number) =>
      (event: KeyboardEvent<HTMLDivElement>) => {
        const lengths: Record<CardColumn, number> = {
          trunk: columns.trunk.length,
          a: columns.a.length,
          b: columns.b.length,
        };
        const move = (nextColumn: CardColumn, nextIndex: number) => {
          event.preventDefault();
          focusCard(nextColumn, nextIndex);
        };

        switch (event.key) {
          case "ArrowDown":
            if (index + 1 < lengths[column]) {
              move(column, index + 1);
            } else if (column === "trunk" && lengths.a > 0) {
              move("a", 0);
            } else if (column === "trunk" && lengths.b > 0) {
              move("b", 0);
            }
            return;
          case "ArrowUp":
            if (index > 0) {
              move(column, index - 1);
            } else if (column !== "trunk" && lengths.trunk > 0) {
              move("trunk", lengths.trunk - 1);
            }
            return;
          case "ArrowRight":
            if (column === "a" && lengths.b > 0) {
              move("b", Math.min(index, lengths.b - 1));
            }
            return;
          case "ArrowLeft":
            if (column === "b" && lengths.a > 0) {
              move("a", Math.min(index, lengths.a - 1));
            }
            return;
          case "Home":
            move(column, 0);
            return;
          case "End":
            move(column, lengths[column] - 1);
            return;
          case " ":
          case "Enter":
            event.preventDefault();
            onSelect(
              column === "trunk"
                ? { type: "select-trunk", count }
                : column === "a"
                  ? { type: "select-left", count }
                  : { type: "select-right", count },
            );
            return;
          default:
        }
      },
    [columns, focusCard, onSelect],
  );

  const renderCard = useCallback(
    (layer: GraphLayer, column: CardColumn, index: number) => {
      const count = column === "trunk" ? index + 1 : trunkLength + index + 1;
      const edge = edgeForCard(column, index);
      const isTrunkChecked =
        column === "trunk" && selection.l === count && selection.r === count;
      return (
        <LayerCard
          key={`${column}:${layer.diffId}:${String(index)}`}
          layer={layer}
          column={column}
          count={count}
          selected={column === "trunk" ? isTrunkChecked || isSelected(column, index) : isSelected(column, index)}
          tabbable={tabIndexes[column] === index}
          maxLayerBytes={graph.maxLayerBytes}
          {...(edge === undefined
            ? {}
            : { edge: { diffIdEqual: edge.diffIdEqual, highlighted: edge.highlighted } })}
          {...(column === "trunk" ? { describedBy: "ll-trunk-hint" } : {})}
          {...(edge === undefined
            ? {}
            : {
                onHoverEdge: (hovered: boolean) => {
                  setHoveredEdge(hovered ? edge.id : null);
                },
              })}
          registerRef={(element) => {
            if (element === null) {
              cardRefs.current.delete(`${column}:${index}`);
            } else {
              cardRefs.current.set(`${column}:${index}`, element);
            }
          }}
          onSelect={() => {
            setTabIndexes((previous) => ({ ...previous, [column]: index }));
            onSelect(
              column === "trunk"
                ? { type: "select-trunk", count }
                : column === "a"
                  ? { type: "select-left", count }
                  : { type: "select-right", count },
            );
          }}
          onKeyDown={handleKeyDown(column, index, count)}
        />
      );
    },
    [
      edgeForCard,
      graph.maxLayerBytes,
      handleKeyDown,
      isSelected,
      onSelect,
      selection.l,
      selection.r,
      tabIndexes,
      trunkLength,
    ],
  );

  /**
   * Which boxes each accent-coloured spine runs through. A fork gets the two
   * branch columns; a strict prefix gets one spine that starts at the last
   * trunk card, so the stack reads as continuous rather than as two piles.
   */
  const spines = useMemo<{ left: Box[]; right: Box[] }>(() => {
    if (shape === "fork") {
      return { left: measured.left, right: measured.right };
    }
    const trunkLast = measured.trunk[measured.trunk.length - 1];
    const join = trunkLast === undefined ? [] : [trunkLast];
    if (shape === "prefix-right") {
      return { left: [], right: [...join, ...measured.right] };
    }
    if (shape === "prefix-left") {
      return { left: [...join, ...measured.left], right: [] };
    }
    return { left: [], right: [] };
  }, [measured.left, measured.right, measured.trunk, shape]);

  const overlayEdges = useMemo<OverlayEdge[]>(() => {
    const result: OverlayEdge[] = [];
    for (const edge of edges) {
      const a = measured.left[edge.leftIndex];
      const b = measured.right[edge.rightIndex];
      if (a === undefined || b === undefined) {
        continue;
      }
      const geometry = couldBeSharedEdge(a, b);
      result.push({
        key: `edge-${String(edge.id)}`,
        d: geometry.d,
        highlighted: hoveredEdge === edge.id,
      });
    }
    return result;
  }, [edges, hoveredEdge, measured.left, measured.right]);

  const pills = useMemo(() => {
    const result: {
      id: number;
      x: number;
      y: number;
      diffIdEqual: boolean;
    }[] = [];
    for (const edge of edges) {
      const a = measured.left[edge.leftIndex];
      const b = measured.right[edge.rightIndex];
      if (a === undefined || b === undefined) {
        continue;
      }
      const geometry = couldBeSharedEdge(a, b);
      result.push({
        id: edge.id,
        x: geometry.pillX,
        y: geometry.pillY,
        diffIdEqual: edge.diffIdEqual,
      });
    }
    return result;
  }, [edges, measured.left, measured.right]);

  const boxForPosition = (position: { column: CardColumn; index: number } | null): Box | null => {
    if (position === null) {
      return null;
    }
    const source =
      position.column === "trunk"
        ? measured.trunk
        : position.column === "a"
          ? measured.left
          : measured.right;
    return source[position.index] ?? null;
  };

  const ruleA = boxForPosition(positionA);
  const ruleB = boxForPosition(positionB);

  const scrollToSelection = (side: "a" | "b") => {
    const position = side === "a" ? positionA : positionB;
    if (position === null) {
      return;
    }
    cardRefs.current.get(`${position.column}:${position.index}`)?.scrollIntoView({
      block: "center",
      behavior: "smooth",
    });
  };

  const leftRef = primaryRef(graph.left);
  const rightRef = primaryRef(graph.right);

  return (
    <section className="flex min-h-0 flex-col" aria-labelledby="layer-section-title">
      <header className="mb-4 flex flex-none flex-wrap items-baseline gap-2.5">
        <h2 id="layer-section-title" className="text-section m-0">
          Layer comparison
        </h2>
        <span className="text-text-muted text-[12px]">base → latest</span>
        <div className="ml-auto flex gap-1.5">
          <button
            type="button"
            className="ll-sel-chip ll-sel-chip-a"
            data-testid="selection-chip-a"
            onClick={() => {
              scrollToSelection("a");
            }}
            title="Scroll to image A's selected layer"
          >
            {selectionLabel("a", selection.l)}
          </button>
          <button
            type="button"
            className="ll-sel-chip ll-sel-chip-b"
            data-testid="selection-chip-b"
            onClick={() => {
              scrollToSelection("b");
            }}
            title="Scroll to image B's selected layer"
          >
            {selectionLabel("b", selection.r)}
          </button>
        </div>
      </header>

      <p id="ll-trunk-hint" className="sr-only">
        Shared layer — selecting it sets both comparison points.
      </p>

      <div className="min-h-0 flex-1 overflow-auto p-0.5">
        <div className="relative pb-9" ref={containerRef} data-testid="layer-diagram">
          <EdgeOverlay
            width={measured.width}
            height={measured.height}
            trunk={measured.trunk}
            left={spines.left}
            right={spines.right}
            couldBeShared={overlayEdges}
            showFork={shape === "fork"}
          />

          {trunkLength === 0 ? (
            <p className="ll-note m-0" data-testid="no-shared-layers" ref={noSharedStripRef}>
              No shared layers — these images share no cached layer, so the comparison forks at
              the base.
            </p>
          ) : (
            <div
              className="relative flex flex-col gap-3.5"
              role="radiogroup"
              aria-label="Shared comparison point (sets both images)"
            >
              {graph.trunk.map((layer, index) => renderCard(layer, "trunk", index))}
            </div>
          )}

          {shape === "identical" ? (
            <p className="ll-note mt-3.5 mb-0" data-testid="identical-note">
              Images are identical at every layer — {leftRef} and {rightRef} resolve to the same
              layer stack.
            </p>
          ) : null}

          {shape === "prefix-left" || shape === "prefix-right" ? (
            <>
              <p className="ll-note mt-3.5 mb-3.5" data-testid="prefix-note">
                {shape === "prefix-right" ? leftRef : rightRef} is fully contained in{" "}
                {shape === "prefix-right" ? rightRef : leftRef}&rsquo;s layer stack — no fork, just
                extra layers on top.
              </p>
              <div
                className="relative flex flex-col gap-3.5"
                role="radiogroup"
                aria-label={
                  shape === "prefix-right"
                    ? "Image B comparison point"
                    : "Image A comparison point"
                }
              >
                {(shape === "prefix-right" ? graph.rightBranch : graph.leftBranch).map(
                  (layer, index) =>
                    renderCard(layer, shape === "prefix-right" ? "b" : "a", index),
                )}
              </div>
            </>
          ) : null}

          {shape === "fork" ? (
            <>
              <div className="my-5 grid grid-cols-2 gap-5">
                <span className="ll-colhead ll-colhead-a">
                  <ImageBadge side="a" />
                  <ImageRefText refName={leftRef} />
                </span>
                <span className="ll-colhead ll-colhead-b">
                  <ImageBadge side="b" />
                  <ImageRefText refName={rightRef} />
                </span>
              </div>

              <div className="grid grid-cols-2 items-start gap-x-5">
                <div
                  className="flex min-w-0 flex-col gap-7"
                  role="radiogroup"
                  aria-label="Image A comparison point"
                >
                  {graph.leftBranch.map((layer, index) => renderCard(layer, "a", index))}
                </div>
                <div
                  className="flex min-w-0 flex-col gap-7"
                  role="radiogroup"
                  aria-label="Image B comparison point"
                >
                  {graph.rightBranch.map((layer, index) => renderCard(layer, "b", index))}
                </div>
              </div>
            </>
          ) : null}

          {pills.map((pill) => (
            <Popover
              key={`pill-${String(pill.id)}`}
              label="Could-be-shared layers"
              side="bottom"
              align="center"
              trigger={
                <button
                  type="button"
                  className="ll-edge-pill"
                  style={{ left: `${String(pill.x)}px`, top: `${String(pill.y)}px` }}
                  onMouseEnter={() => {
                    setHoveredEdge(pill.id);
                  }}
                  onMouseLeave={() => {
                    setHoveredEdge(null);
                  }}
                  data-testid="could-be-shared-pill"
                >
                  ≈ same content
                </button>
              }
            >
              <CouldBeSharedExplanation diffIdEqual={pill.diffIdEqual} />
            </Popover>
          ))}

          {ruleA === null ? null : (
            <div
              className="ll-sel-rule ll-sel-rule-a"
              aria-hidden="true"
              data-testid="selection-rule-a"
              style={{
                top: `${String(selectionRuleBox(ruleA).top)}px`,
                left: `${String(selectionRuleBox(ruleA).left)}px`,
                width: `${String(selectionRuleBox(ruleA).width)}px`,
              }}
            >
              <span style={positionA?.column === "trunk" ? { left: 0 } : { right: 0 }}>
                {selectionLabel("a", selection.l)}
              </span>
            </div>
          )}
          {ruleB === null ? null : (
            <div
              className="ll-sel-rule ll-sel-rule-b"
              aria-hidden="true"
              data-testid="selection-rule-b"
              style={{
                top: `${String(selectionRuleBox(ruleB).top)}px`,
                left: `${String(selectionRuleBox(ruleB).left)}px`,
                width: `${String(selectionRuleBox(ruleB).width)}px`,
              }}
            >
              <span style={{ right: 0 }}>{selectionLabel("b", selection.r)}</span>
            </div>
          )}
        </div>
      </div>

      <p aria-live="polite" className="sr-only">
        {`Comparing ${selectionLabel("a", selection.l)} against ${selectionLabel("b", selection.r)}`}
      </p>
      <span className="sr-only">{`${String(bounds.leftLength)} layers in image A, ${String(bounds.rightLength)} in image B`}</span>
    </section>
  );
}
