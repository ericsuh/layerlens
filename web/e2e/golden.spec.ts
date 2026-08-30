import { expect, test } from "@playwright/test";

import { cellEdges, expandPath, expandRow, headerEdges, revealRow, row } from "./helpers";

/**
 * PROJECT.md's acceptance criterion, asserted step by step against the real
 * binary serving the vendored fixtures — no Docker, no network, no mocks.
 *
 * A @ layer 7 (`RUN npm install`) vs B @ layer 8 (`RUN apt-get … && rm -rf`)
 * is one layer from each branch and is the selection that shows all three diff
 * polarities at once: v2's stray `debug.log` added, `main.js` modified, and
 * the apt cleanup's whiteouts as removals.
 */
test("golden workflow: pick two images, read the layer graph, diff the filesystems", async ({
  page,
}) => {
  await test.step("the app opens on the selection view with the demo images listed", async () => {
    await page.goto("/");
    await expect(page.getByTestId("analyzed-row-example:v1")).toBeVisible();
    await expect(page.getByTestId("analyzed-row-example:v2")).toBeVisible();
    await expect(page.getByTestId("compare-button")).toBeDisabled();
  });

  await test.step("picking example:v1 then example:v2 fills slot A then slot B", async () => {
    await page.getByTestId("analyzed-row-example:v1").click();
    await expect(page.getByTestId("slot-a")).toContainText("example:v1");
    await page.getByTestId("analyzed-row-example:v2").click();
    await expect(page.getByTestId("slot-b")).toContainText("example:v2");
    await expect(page.getByTestId("compare-button")).toBeEnabled();
    await page.getByTestId("compare-button").click();
    await expect(page).toHaveURL(/\/compare\?/);
  });

  await test.step("the layer view shows a shared trunk, a fork and two attributed branches", async () => {
    // Five shared layers: base rootfs, apt deps, node, yarn, WORKDIR.
    await expect(page.getByTestId("layer-card-trunk-1")).toBeVisible();
    await expect(page.getByTestId("layer-card-trunk-5")).toBeVisible();
    await expect(page.getByTestId("layer-card-trunk-6")).toHaveCount(0);
    await expect(page.getByTestId("layer-card-a-6")).toContainText("COPY");
    await expect(page.getByTestId("layer-card-b-6")).toContainText("COPY");
    await expect(page.getByTestId("layer-card-a-7")).toContainText("npm install");
    await expect(page.getByTestId("fork-a")).toBeVisible();
    await expect(page.getByTestId("fork-b")).toBeVisible();
  });

  await test.step("a dotted could-be-shared edge links the two npm install layers", async () => {
    const edges = page.getByTestId("could-be-shared-edge");
    await expect(edges.first()).toBeVisible();
    // Dotted, never solid: these layers were both built, they are not a cache
    // hit, and the stroke has to say so without relying on words.
    await expect(edges.first()).toHaveClass(/ll-edge-dotted/);
    await expect(page.getByTestId("could-be-shared-pill").first()).toContainText("same content");
  });

  await test.step("selecting one layer per branch loads the filesystem diff", async () => {
    await page.getByTestId("layer-card-a-7").click();
    await page.getByTestId("layer-card-b-8").click();
    await expect(page.getByTestId("fs-compare-a")).toHaveText("A @ layer 7");
    await expect(page.getByTestId("fs-compare-b")).toHaveText("B @ layer 8");
    await expect(page).toHaveURL(/l=7&r=8/);
  });

  await test.step("the tree shows additions, a modification and the apt whiteout removals", async () => {
    // /app is opened automatically: with "changed only" the whole diff hangs
    // off it, and one collapsed row is not a first paint.
    await expect(row(page, "/app")).toHaveAttribute("aria-expanded", "true");

    const debugLog = row(page, "/app/debug.log");
    await expect(debugLog).toHaveAttribute("data-status", "added");
    await expect(debugLog).toHaveAttribute("aria-label", /added/);
    await expect(debugLog.getByTestId("cell-delta-size")).toHaveText("+158 KiB");

    const mainJs = row(page, "/app/main.js");
    await expect(mainJs).toHaveAttribute("data-status", "modified");
    await expect(mainJs.getByTestId("cell-delta-size")).toHaveText("+695 B");

    // The apt cleanup: `rm -rf /var/lib/{apt,dpkg,cache,log}` over directories
    // the base layer shipped, which the diff sees as removals.
    await expandPath(page, "/var/lib");
    for (const name of ["apt", "cache", "dpkg", "log"]) {
      const removed = await revealRow(page, `/var/lib/${name}`);
      await expect(removed).toHaveAttribute("data-status", "removed");
      await expect(removed).toHaveAttribute("aria-label", /removed/);
    }
  });

  await test.step("folder rows carry human sizes, counts, deltas and a relative-size bar", async () => {
    const app = await revealRow(page, "/app");
    await expect(app.getByTestId("cell-size")).toHaveText(/^\d+(\.\d)? (B|KiB|MiB|GiB)$/);
    await expect(app.getByTestId("cell-files")).toHaveText("307");
    await expect(app.getByTestId("cell-delta-size")).toHaveText("+4.8 MiB");
    await expect(app.getByTestId("cell-delta-files")).toHaveText("+58");
    await expect(app.getByTestId("size-bar")).toBeVisible();
    // Never colour alone: a directory containing changes shows how many.
    await expect(app.getByTestId("cell-delta-size")).toBeVisible();
    await expect(app).toHaveAttribute("data-status", "contains");
  });

  await test.step("the column header is sticky and stays aligned at depth", async () => {
    await page.getByTestId("filter-select").selectOption("all");
    await expandPath(page, "/app/node_modules/@babel/plugin-transform-runtime/lib/get-runtime-path");
    const deepest = await revealRow(
      page,
      "/app/node_modules/@babel/plugin-transform-runtime/lib/get-runtime-path/index.js",
    );
    await expect(page.getByTestId("tree-header")).toBeInViewport();
    const header = await headerEdges(page);
    const cells = await cellEdges(deepest);
    expect(cells.length).toBe(header.length);
    // The Name column is fluid and absorbs the indent; every numeric column
    // must line up with its header to the pixel.
    expect(cells.slice(1)).toEqual(header.slice(1));
  });

  await test.step("disclosure expands in place; drill-down re-roots with breadcrumbs", async () => {
    await page.getByTestId("filter-select").selectOption("changed");
    await expandRow(page, "/app");
    const src = await revealRow(page, "/app/src");
    await src.hover();
    await src.getByRole("button", { name: "Open /app/src as root" }).click();
    await expect(page).toHaveURL(/path=\/app\/src/);
    await expect(page.getByTestId("crumb-current")).toHaveText("src");
    // Re-rooted: the row is now a top-level entry, not a nested one.
    await expect(row(page, "/app/src/util.js")).toHaveAttribute("data-level", "0");
    // And the breadcrumb walks back up.
    await page.getByRole("button", { name: "app", exact: true }).click();
    await expect(page).toHaveURL(/path=\/app/);
    await expect(page.getByTestId("crumb-current")).toHaveText("app");
  });

  await test.step("the depth=2 prefetch makes the first expand cost no request", async () => {
    // The root page arrives with one level of grandchildren embedded (§8.4),
    // so opening a directory whose children all fit is a render.
    const fresh = await page.context().newPage();
    const requested: string[] = [];
    fresh.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname.endsWith("/diff/tree")) {
        requested.push(url.searchParams.get("path") ?? "/");
      }
    });
    await fresh.goto(page.url().replace(/path=[^&]*/, "path=/app"));
    await expect(fresh.locator('[data-testid="tree-row-/app/src"]')).toBeVisible();
    requested.length = 0;
    await fresh.locator('[data-testid="tree-row-/app/src"] button.ll-chev').click();
    await expect(fresh.locator('[data-testid="tree-row-/app/src/util.js"]')).toBeVisible();
    expect(requested).toEqual([]);
    await fresh.close();
  });

  await test.step("the URL alone reproduces the view in a fresh page", async () => {
    const shared = page.url();
    const fresh = await page.context().newPage();
    await fresh.goto(shared);
    await expect(fresh.getByTestId("fs-compare-a")).toHaveText("A @ layer 7");
    await expect(fresh.getByTestId("fs-compare-b")).toHaveText("B @ layer 8");
    await expect(fresh.getByTestId("crumb-current")).toHaveText("app");
    await expect(fresh.locator('[data-testid="tree-row-/app/debug.log"]')).toBeVisible();
    await fresh.close();
  });
});
