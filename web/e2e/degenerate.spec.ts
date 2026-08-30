import { expect, test } from "@playwright/test";

import { compareUrl, imageIds, row } from "./helpers";

/**
 * The comparison shapes that are not the golden one: a strict-prefix pair, a
 * pair with no shared layers at all, and the deliberate self-diff you get by
 * putting both comparison points on the same shared trunk layer (RESEARCH
 * Q11) — the case that proves layer sharing by producing nothing to show.
 */
test.describe("degenerate comparisons", () => {
  test("a strict-prefix pair renders an empty branch and still selects", async ({
    page,
    request,
  }) => {
    const ids = await imageIds(request);
    await page.goto(
      compareUrl({ left: ids["prefix:base"] ?? "", right: ids["prefix:extended"] ?? "" }),
    );

    // base is extended's own trunk: three shared layers, an empty A branch.
    await expect(page.getByTestId("layer-card-trunk-3")).toBeVisible();
    await expect(page.getByTestId("prefix-note")).toBeVisible();
    await expect(page.getByTestId("layer-card-a-4")).toHaveCount(0);
    await expect(page.getByTestId("layer-card-b-4")).toBeVisible();

    await page.getByTestId("layer-card-b-4").click();
    await expect(page.getByTestId("fs-compare-b")).toHaveText("B @ layer 4");
    await expect(page.getByTestId("tree-rows")).toBeVisible();
  });

  test("a disjoint pair forks from the root with no trunk", async ({ page, request }) => {
    const ids = await imageIds(request);
    await page.goto(compareUrl({ left: ids["disjoint:a"] ?? "", right: ids["disjoint:b"] ?? "" }));

    await expect(page.getByTestId("no-shared-layers")).toBeVisible();
    await expect(page.getByTestId("layer-card-trunk-1")).toHaveCount(0);
    await expect(page.getByTestId("layer-card-a-1")).toBeVisible();
    await expect(page.getByTestId("layer-card-b-1")).toBeVisible();
  });

  test("a trunk point compares a layer to itself and says so (states #25/#26)", async ({
    page,
    request,
  }) => {
    const ids = await imageIds(request);
    await page.goto(
      compareUrl({ left: ids["example:v1"] ?? "", right: ids["example:v2"] ?? "", l: 4, r: 4 }),
    );

    // Changed-only is the default, and there is nothing changed to show.
    await expect(page.getByTestId("tree-empty-identical")).toBeVisible();
    await expect(page.getByTestId("tree-empty-identical")).toContainText("No differences");

    // "Show all" is the escape hatch: the filesystem is still browsable.
    await page.getByRole("button", { name: "Show all entries" }).click();
    await expect(page).toHaveURL(/filter=all/);
    await expect(row(page, "/usr")).toBeVisible();
    await expect(row(page, "/usr")).toHaveAttribute("data-status", "unchanged");
  });

  test("filtering a directory down to nothing explains what is hidden (state #26)", async ({
    page,
    request,
  }) => {
    const ids = await imageIds(request);
    await page.goto(
      compareUrl({
        left: ids["example:v1"] ?? "",
        right: ids["example:v2"] ?? "",
        l: 7,
        r: 8,
        path: "/usr/local/lib",
        filter: "changed",
      }),
    );
    const empty = page.getByTestId("tree-empty-filtered");
    await expect(empty).toBeVisible();
    // The count comes from a real `filter=all` request, not from a guess — and
    // that request also warms the cache the button switches to.
    await expect(empty).toContainText(/\d+ unchanged (entry is|entries are) hidden/);
    await page.getByRole("button", { name: "Show all entries" }).click();
    await expect(page.getByTestId("tree-rows")).toBeVisible();
  });

  test("the name filter reports its own emptiness (state #29)", async ({ page, request }) => {
    const ids = await imageIds(request);
    await page.goto(
      compareUrl({ left: ids["example:v1"] ?? "", right: ids["example:v2"] ?? "", l: 7, r: 8 }),
    );
    await expect(row(page, "/app")).toBeVisible();
    await page.getByTestId("name-filter").fill("nothing-matches-this");
    await expect(page.getByTestId("tree-empty-name")).toBeVisible();
    await expect(page.getByTestId("tree-empty-name")).toContainText("nothing-matches-this");
  });

  test("the name filter reaches into directories that are only prefetched", async ({
    page,
    request,
  }) => {
    const ids = await imageIds(request);
    await page.goto(
      compareUrl({
        left: ids["example:v1"] ?? "",
        right: ids["example:v2"] ?? "",
        l: 7,
        r: 8,
        path: "/app",
        filter: "all",
      }),
    );
    const src = row(page, "/app/src");
    await expect(src).toBeVisible();
    await expect(src).toHaveAttribute("aria-expanded", "false");

    // `src/` is collapsed, but the depth=2 prefetch already knows its
    // children, so a search for "util" finds them and opens the ancestor
    // chain to reach them (DESIGN §5.3).
    await page.getByTestId("name-filter").fill("util");
    await expect(row(page, "/app/src/util.js")).toBeVisible();
    await expect(row(page, "/app/src/old-util.js")).toHaveAttribute("data-status", "removed");
    await expect(src).toHaveAttribute("aria-expanded", "true");
  });

  test("a path that is a directory on one side and a file on the other is labelled", async ({
    page,
    request,
  }) => {
    const ids = await imageIds(request);
    await page.goto(
      compareUrl({
        left: ids["edgecase:opaque"] ?? "",
        right: ids["edgecase:plain"] ?? "",
        path: "/etc",
        filter: "all",
      }),
    );
    const config = row(page, "/etc/config");
    await expect(config).toBeVisible();
    await expect(config.getByText("type changed")).toBeVisible();
  });
});
