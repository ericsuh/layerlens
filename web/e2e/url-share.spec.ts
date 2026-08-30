import { expect, test } from "@playwright/test";

import { compareUrl, imageIds, row } from "./helpers";

/**
 * Shareability is an acceptance criterion, not a nicety: the URL carries the
 * pair, both comparison points, the drill-down root and the filter, and a
 * fresh page given only that URL must land on the identical view.
 */
test("the full comparison state round-trips through the URL", async ({ page, request }) => {
  const ids = await imageIds(request);
  const url = compareUrl({
    left: ids["example:v1"] ?? "",
    right: ids["example:v2"] ?? "",
    l: 7,
    r: 8,
    path: "/app/src",
    filter: "all",
  });

  await page.goto(url);
  await expect(page.getByTestId("fs-compare-a")).toHaveText("A @ layer 7");
  await expect(page.getByTestId("fs-compare-b")).toHaveText("B @ layer 8");
  await expect(page.getByTestId("crumb-current")).toHaveText("src");
  await expect(page.getByTestId("filter-select")).toHaveValue("all");
  await expect(page.getByTestId("layer-card-a-7")).toHaveAttribute("aria-checked", "true");
  await expect(page.getByTestId("layer-card-b-8")).toHaveAttribute("aria-checked", "true");
  const before = await page
    .locator('[data-testid^="tree-row-"]')
    .evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-testid")));

  const fresh = await page.context().newPage();
  await fresh.goto(url);
  await expect(fresh.getByTestId("crumb-current")).toHaveText("src");
  const after = await fresh
    .locator('[data-testid^="tree-row-"]')
    .evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-testid")));
  expect(after).toEqual(before);
  await fresh.close();
});

test("a hand-edited filter value falls back to the default rather than breaking", async ({
  page,
  request,
}) => {
  const ids = await imageIds(request);
  await page.goto(
    `${compareUrl({ left: ids["example:v1"] ?? "", right: ids["example:v2"] ?? "", l: 7, r: 8 })}&filter=bogus`,
  );
  await expect(page.getByTestId("filter-select")).toHaveValue("changed");
  await expect(row(page, "/app")).toBeVisible();
});
