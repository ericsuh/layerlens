import { expect } from "@playwright/test";
import type { APIRequestContext, Locator, Page } from "@playwright/test";

/** The image ids behind the fixture tags, read from the running server. */
export async function imageIds(request: APIRequestContext): Promise<Record<string, string>> {
  const response = await request.get("/api/v1/images");
  expect(response.ok()).toBe(true);
  const body = (await response.json()) as {
    images: { id: string; refNames: string[] }[];
  };
  const ids: Record<string, string> = {};
  for (const image of body.images) {
    for (const ref of image.refNames) {
      ids[ref] = image.id;
    }
  }
  return ids;
}

export interface CompareParams {
  left: string;
  right: string;
  l?: number;
  r?: number;
  path?: string;
  filter?: string;
}

export function compareUrl(params: CompareParams): string {
  const search = new URLSearchParams({ left: params.left, right: params.right });
  if (params.l !== undefined) search.set("l", String(params.l));
  if (params.r !== undefined) search.set("r", String(params.r));
  if (params.path !== undefined) search.set("path", params.path);
  if (params.filter !== undefined) search.set("filter", params.filter);
  return `/compare?${search.toString()}`;
}

export function row(page: Page, path: string): Locator {
  return page.locator(`[data-testid="tree-row-${path}"]`);
}

/**
 * Scrolls the tree until a row is in the DOM.
 *
 * The tree is virtualized, so "not found" and "below the fold" look identical
 * to a locator — every helper that touches a row has to go through here rather
 * than assume the row is rendered.
 */
export async function revealRow(page: Page, path: string): Promise<Locator> {
  const target = row(page, path);
  const scroller = page.getByTestId("tree-scroll");
  await expect(scroller).toBeVisible();
  // Several sweeps, not one: a directory's rows can still be in flight (the
  // panel shows the previous filter's rows while the new ones load), so a
  // single pass can legitimately miss a row that exists.
  for (let sweep = 0; sweep < 4; sweep += 1) {
    await scroller.evaluate((element) => {
      element.scrollTop = 0;
    });
    for (let step = 0; step < 400; step += 1) {
      if ((await target.count()) > 0) {
        await target.scrollIntoViewIfNeeded();
        return target;
      }
      const atBottom = await scroller.evaluate((element) => {
        const before = element.scrollTop;
        element.scrollTop += element.clientHeight * 0.75;
        return element.scrollTop === before;
      });
      await page.waitForTimeout(60);
      if (atBottom) {
        break;
      }
    }
    await page.waitForTimeout(300);
  }
  await expect(target).toBeVisible();
  return target;
}

/** Expands a directory row by its disclosure triangle (not by drilling in). */
export async function expandRow(page: Page, path: string): Promise<void> {
  const target = await revealRow(page, path);
  if ((await target.getAttribute("aria-expanded")) === "false") {
    await target.locator("button.ll-chev").click();
  }
  await expect(target).toHaveAttribute("aria-expanded", "true");
}

/** Walks a chain of directories open, top down. */
export async function expandPath(page: Page, deepest: string): Promise<void> {
  const segments = deepest.split("/").filter((segment) => segment !== "");
  let accumulated = "";
  for (const segment of segments) {
    accumulated += `/${segment}`;
    await expandRow(page, accumulated);
  }
}

/** The x-extents of every rendered cell of a row, left to right. */
export async function cellEdges(target: Locator): Promise<[number, number][]> {
  return target.locator("> div").evaluateAll((nodes) =>
    nodes
      .filter((node) => getComputedStyle(node).display !== "none")
      .map((node) => {
        const box = node.getBoundingClientRect();
        return [Math.round(box.left), Math.round(box.right)] as [number, number];
      }),
  );
}

/** The same extents for the sticky header's cells. */
export async function headerEdges(page: Page): Promise<[number, number][]> {
  return page
    .locator('[data-testid="tree-header"] > div')
    .evaluateAll((nodes) =>
      nodes
        .filter((node) => getComputedStyle(node).display !== "none")
        .map((node) => {
          const box = node.getBoundingClientRect();
          return [Math.round(box.left), Math.round(box.right)] as [number, number];
        }),
    );
}
