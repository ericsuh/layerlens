import { expect, test } from "@playwright/test";

import { compareUrl, imageIds } from "./helpers";

/**
 * The `wide:*` fixture is one directory with 2 500 children — 13 pages at the
 * default limit of 200. It is the only fixture that exercises the two things
 * that only matter at scale: cursor paging without duplicates, and a
 * virtualized list that stays bounded while it scrolls.
 */
test.describe("wide directory", () => {
  test("pages in on scroll until every row is loaded, with no duplicates", async ({
    page,
    request,
  }) => {
    const ids = await imageIds(request);
    await page.goto(
      compareUrl({
        left: ids["wide:v1"] ?? "",
        right: ids["wide:v2"] ?? "",
        path: "/data/shards",
        filter: "all",
      }),
    );

    const showing = page.getByTestId("showing-count");
    await expect(showing).toHaveText("showing 200 of 2,500 entries");

    const scroller = page.getByTestId("tree-scroll");
    // Scrolling to the tail brings state #28's trailing row into the list. It
    // is a button, and it is also what the watermark drives, so at any given
    // instant it reads either its resting label or its in-flight one.
    await scroller.evaluate((element) => {
      element.scrollTop = element.scrollHeight;
    });
    await expect(page.getByTestId("show-more")).toHaveText(/Show [\d,]+ more…|Loading more…/);

    for (let step = 0; step < 120; step += 1) {
      const text = await showing.textContent();
      if (text === "showing 2,500 of 2,500 entries") {
        break;
      }
      await scroller.evaluate((element) => {
        element.scrollTop = element.scrollHeight;
      });
      await page.waitForTimeout(120);
    }
    await expect(showing).toHaveText("showing 2,500 of 2,500 entries");
    // Every page loaded means the trailer is gone.
    await expect(page.getByTestId("show-more")).toHaveCount(0);

    const paths = await page
      .locator('[data-testid^="tree-row-"]')
      .evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-testid")));
    expect(new Set(paths).size).toBe(paths.length);
  });

  test("virtualization keeps the DOM bounded while scrolling 2,500 rows", async ({
    page,
    request,
  }) => {
    const ids = await imageIds(request);
    await page.goto(
      compareUrl({
        left: ids["wide:v1"] ?? "",
        right: ids["wide:v2"] ?? "",
        path: "/data/shards",
        filter: "all",
      }),
    );
    await expect(page.getByTestId("showing-count")).toHaveText("showing 200 of 2,500 entries");

    const scroller = page.getByTestId("tree-scroll");
    const rows = page.locator('[data-testid^="tree-row-"]');
    let peak = 0;
    for (let step = 0; step < 40; step += 1) {
      await scroller.evaluate((element) => {
        element.scrollTop += element.clientHeight;
      });
      await page.waitForTimeout(80);
      peak = Math.max(peak, await rows.count());
    }
    // A ~640px viewport of 32px rows is ~20 rows; with 8 rows of overscan on
    // each side, anything near 2 500 would mean virtualization is not
    // happening at all.
    expect(peak).toBeGreaterThan(10);
    expect(peak).toBeLessThan(80);
    // …and the list really did grow behind the window.
    expect(Number(await page.getByTestId("tree-rows").getAttribute("data-row-count"))).toBeGreaterThan(
      200,
    );
  });

  test("a failed page keeps the loaded rows and offers Retry in place (state #27)", async ({
    page,
    request,
  }) => {
    const ids = await imageIds(request);
    // Only *cursor* requests fail: the first page must succeed so there are
    // rows to preserve, which is the whole point of the state.
    let failNext = true;
    await page.route("**/api/v1/diff/tree?**", async (route) => {
      if (failNext && route.request().url().includes("cursor=")) {
        failNext = false;
        await route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "internal", message: "The comparison store is unavailable." },
          }),
        });
        return;
      }
      await route.continue();
    });

    await page.goto(
      compareUrl({
        left: ids["wide:v1"] ?? "",
        right: ids["wide:v2"] ?? "",
        path: "/data/shards",
        filter: "all",
      }),
    );
    const showing = page.getByTestId("showing-count");
    await expect(showing).toHaveText("showing 200 of 2,500 entries");

    const scroller = page.getByTestId("tree-scroll");
    await scroller.evaluate((element) => {
      element.scrollTop = element.scrollHeight;
    });

    const errorRow = page.getByTestId("tree-row-error");
    await expect(errorRow).toBeVisible();
    await expect(errorRow).toContainText("The comparison store is unavailable.");
    // The 200 rows that did load are still there — a failed page is not a
    // failed directory.
    await expect(showing).toHaveText("showing 200 of 2,500 entries");

    await errorRow.getByRole("button", { name: "Retry" }).click();
    await expect(showing).toHaveText("showing 400 of 2,500 entries");
  });
});
