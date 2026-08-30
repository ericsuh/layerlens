import { expect, test } from "@playwright/test";

import {
  cellEdges,
  compareUrl,
  expandPath,
  expandRow,
  headerEdges,
  imageIds,
  revealRow,
  row,
} from "./helpers";

/**
 * The three tree-view fixes the design review made acceptance criteria
 * (RESEARCH Q12). Each is asserted against the rendered page — computed
 * geometry and computed style — rather than against the stylesheet, because
 * "the CSS says nowrap" and "the cell does not wrap" are different claims.
 */
const DEEP = "/app/node_modules/@babel/plugin-transform-runtime/lib/get-runtime-path";

async function openDeepTree(page: import("@playwright/test").Page, ids: Record<string, string>) {
  await page.goto(
    compareUrl({
      left: ids["example:v1"] ?? "",
      right: ids["example:v2"] ?? "",
      l: 7,
      r: 8,
      filter: "all",
    }),
  );
  await expect(row(page, "/app")).toBeVisible();
  await expandPath(page, DEEP);
  return revealRow(page, `${DEEP}/index.js`);
}

test("fix 1 — the header row is sticky and pixel-aligned with rows at every depth", async ({
  page,
  request,
}) => {
  const ids = await imageIds(request);
  const deepest = await openDeepTree(page, ids);
  await expect(deepest).toHaveAttribute("data-level", "6");

  const header = page.getByTestId("tree-header");
  await expect(header).toHaveCSS("position", "sticky");

  // Sticky in fact, not just in declaration: after scrolling the tree, the
  // header is still flush with the top of its scroll container.
  const scrollerBox = await page.getByTestId("tree-scroll").boundingBox();
  const headerBox = await header.boundingBox();
  expect(headerBox?.y).toBeCloseTo(scrollerBox?.y ?? -1, 0);

  const headerCells = await headerEdges(page);
  for (const path of ["/app", `${DEEP}`, `${DEEP}/index.js`]) {
    const target = await revealRow(page, path);
    const cells = await cellEdges(target);
    expect(cells.length).toBe(headerCells.length);
    // The Name cell is fluid and swallows the indentation; every other column
    // is a fixed grid track and must match the header exactly.
    expect(cells.slice(1)).toEqual(headerCells.slice(1));
  }
});

test("fix 2 — numeric columns reserve their worst case and never wrap", async ({
  page,
  request,
}) => {
  const ids = await imageIds(request);
  const deepest = await openDeepTree(page, ids);

  // cell-size is a two-row flex cell (number over bar), so its own box is not
  // the thing that must not wrap — the number inside it is.
  const numeric = ["cell-delta-size", "cell-delta-files", "cell-files"] as const;
  for (const id of [...numeric, "cell-size"] as const) {
    const cell =
      id === "cell-size"
        ? deepest.getByTestId(id).locator(".ll-tnum")
        : deepest.getByTestId(id);
    await expect(cell).toHaveCSS("white-space", "nowrap");
    await expect(cell).toHaveCSS("text-align", "right");
    await expect(cell).toHaveCSS("font-variant-numeric", "tabular-nums");
  }

  // Worst-case content, injected: the fixtures are far too small to produce
  // `−1023.9 MiB` or `+999,999`, and the columns were sized for the worst
  // plausible image, not for this one.
  // Keyed by the selector that actually holds the number: cell-size wraps its
  // value in .ll-tnum so the bar can sit beneath it, and measuring the flex
  // container instead would measure the bar's track rather than the text.
  const worstCase: Record<string, string> = {
    '[data-testid="cell-delta-size"]': "−1023.9 MiB",
    '[data-testid="cell-delta-files"]': "+999,999",
    '[data-testid="cell-size"] .ll-tnum': "1023.9 MiB",
    '[data-testid="cell-files"]': "999,999",
  };
  const overflow = await deepest.evaluate((rowElement, values) => {
    const results: { id: string; overflowed: boolean; lines: number }[] = [];
    for (const [id, text] of Object.entries(values)) {
      const cell = rowElement.querySelector<HTMLElement>(id);
      if (cell === null) {
        continue;
      }
      const original = cell.textContent;
      cell.textContent = text;
      const lineHeight = Number.parseFloat(getComputedStyle(cell).lineHeight) || 16;
      results.push({
        id,
        // scrollWidth > clientWidth means the text is being clipped by the
        // reserved track — i.e. the reservation was too small.
        overflowed: cell.scrollWidth > cell.clientWidth,
        lines: Math.round(cell.scrollHeight / lineHeight),
      });
      cell.textContent = original;
    }
    return results;
  }, worstCase);

  expect(overflow.length).toBe(Object.keys(worstCase).length);
  for (const result of overflow) {
    expect(result, `${result.id} must fit its reserved width`).toMatchObject({
      overflowed: false,
      lines: 1,
    });
  }
});

test("fix 3 — columns are labelled, and no cell carries unit-suffix jargon", async ({
  page,
  request,
}) => {
  const ids = await imageIds(request);
  await openDeepTree(page, ids);

  const labels = await page
    .locator('[data-testid="tree-header"] > div')
    .evaluateAll((nodes) => nodes.map((node) => node.textContent?.trim() ?? ""));
  expect(labels).toEqual(["Name", "±", "Size", "Δ size", "Files", "Δ files"]);

  for (const [id, title] of [
    ["col-status", /Change status/],
    ["col-deltaSize", /Change in total size, B relative to A/],
    ["col-deltaFiles", /Change in file count, B relative to A/],
    ["col-size", /Total size in image B, with a bar scaled against the largest top-level entry/],
    ["col-files", /Total file count in image B/],
  ] as const) {
    await expect(page.getByTestId(id)).toHaveAttribute("title", title);
  }

  // The composite `142 MiB · 393 f` cell is gone: no cell may pair a size with
  // a bare count, and no count may carry an "f"/"files" suffix.
  const cells = await page
    .locator('[data-testid^="cell-"]')
    .evaluateAll((nodes) => nodes.map((node) => node.textContent?.trim() ?? ""));
  expect(cells.length).toBeGreaterThan(20);
  for (const text of cells) {
    expect(text).not.toMatch(/\d\s*f\b/);
    expect(text).not.toMatch(/\d\s+files?\b/);
    expect(text).not.toMatch(/·/);
  }
});

test("below 1280px both file-count columns leave the header and the rows together", async ({
  page,
  request,
}) => {
  const ids = await imageIds(request);
  await page.setViewportSize({ width: 1100, height: 900 });
  const deepest = await openDeepTree(page, ids);

  await expect(page.getByTestId("col-deltaFiles")).toBeHidden();
  await expect(page.getByTestId("col-files")).toBeHidden();
  await expect(deepest.getByTestId("cell-delta-files")).toBeHidden();
  await expect(deepest.getByTestId("cell-files")).toBeHidden();

  // Four columns left, and still aligned.
  const headerCells = await headerEdges(page);
  expect(headerCells.length).toBe(4);
  expect((await cellEdges(deepest)).slice(1)).toEqual(headerCells.slice(1));

  // The data did not disappear — it is in the row's screen-reader sentence.
  await expect(deepest).toHaveAttribute("aria-label", /total/);
});

test("the tree is a keyboard-navigable ARIA tree", async ({ page, request }) => {
  const ids = await imageIds(request);
  await page.goto(
    compareUrl({ left: ids["example:v1"] ?? "", right: ids["example:v2"] ?? "", l: 7, r: 8 }),
  );
  const app = row(page, "/app");
  await expect(app).toBeVisible();
  await expect(page.getByRole("tree", { name: "Filesystem diff" })).toBeVisible();
  await expect(app).toHaveAttribute("aria-level", "1");
  await expect(app).toHaveAttribute("aria-posinset", "1");
  // The server's post-filter child count of the root, not the number of rows
  // this client has paged in.
  await expect(app).toHaveAttribute("aria-setsize", "4");
  await expandRow(page, "/app");

  await app.focus();
  await page.keyboard.press("ArrowDown");
  await expect(row(page, "/app/.git")).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await expect(row(page, "/app/.git")).toHaveAttribute("aria-expanded", "true");
  await page.keyboard.press("ArrowLeft");
  await expect(row(page, "/app/.git")).toHaveAttribute("aria-expanded", "false");
});
