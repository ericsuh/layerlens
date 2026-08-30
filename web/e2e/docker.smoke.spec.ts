import { expect, test } from "@playwright/test";

/**
 * The opt-in Docker smoke test: analyze whatever image the local daemon
 * happens to hold. Off by default — the default suite must pass on a machine
 * with no socket at all. Run it with `E2E_DOCKER=1 mise run e2e`.
 */
test.describe("docker daemon ingest", () => {
  test.skip(process.env.E2E_DOCKER !== "1", "set E2E_DOCKER=1 to analyze a local image");
  test.setTimeout(180_000);

  test("analyzes a local image and makes it selectable", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("tab", { name: /Docker daemon/ }).click();

    const rows = page.locator('[data-testid^="docker-row-"]');
    await expect(rows.first()).toBeVisible();

    const analyze = page.getByRole("button", { name: "Analyze" }).first();
    if ((await analyze.count()) === 0) {
      // Everything the daemon holds is already analyzed: the row is a slot
      // target, which is the state the Analyze button exists to reach.
      await rows.first().click();
      await expect(page.getByTestId("slot-a")).not.toContainText("Select an image below");
      return;
    }

    await analyze.click();

    const card = page.getByRole("group", { name: /^Pull of / });
    await expect(card).toBeVisible();
    await expect(card.getByTestId("pull-error")).toHaveCount(0, { timeout: 150_000 });
    await expect(rows.first()).toHaveAttribute("role", "button", { timeout: 150_000 });
  });
});
