import { expect, test } from "@playwright/test";

/**
 * The opt-in registry smoke test: one real, tiny, public image pulled end to
 * end through the UI.
 *
 * Off by default because the default suite is hermetic (no network, no Docker)
 * and because Docker Hub rate-limits anonymous pulls — a limit that would
 * otherwise fail this suite for a reason that has nothing to do with the code.
 * Run it with `E2E_NETWORK=1 mise run e2e`.
 */
test.describe("registry pull (network)", () => {
  test.skip(process.env.E2E_NETWORK !== "1", "set E2E_NETWORK=1 to pull a real image");
  test.setTimeout(180_000);

  test("pulls a public image and lands it in the Analyzed tab", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("tab", { name: /Registry/ }).click();
    await page.getByTestId("registry-input").fill("alpine:3.20");
    await expect(page.getByTestId("registry-verdict")).toContainText("index.docker.io");
    await page.getByTestId("registry-submit").click();

    // The server keys a pull on the *canonical* reference (its idempotency
    // key), so the card is named "index.docker.io/library/alpine:3.20" rather
    // than what was typed — located by its accessible name, not by guessing.
    const card = page.getByRole("group", { name: /alpine/ });
    await expect(card).toBeVisible();
    await expect(card.getByTestId("pull-steps")).toBeVisible();

    // Waiting happens from the Analyzed tab: only the active panel is
    // rendered, so the row this pull is meant to produce cannot appear while
    // the Registry form is on screen. The pull card is above the tab strip, so
    // a failure is still visible from here.
    await page.getByRole("tab", { name: /Analyzed/ }).click();
    const failure = card.getByTestId("pull-error");
    const analyzed = page
      .locator('[data-testid^="analyzed-row-"]')
      .filter({ hasText: "alpine:3.20" });

    await expect(failure.or(analyzed).first()).toBeVisible({ timeout: 150_000 });
    if (await failure.isVisible()) {
      const message = (await failure.textContent()) ?? "";
      test.skip(message.includes("rate limit"), "Docker Hub rate limit reached");
      throw new Error(`pull failed: ${message}`);
    }

    // The pulled image is a first-class analyzed image: selectable like any other.
    await analyzed.first().click();
    await expect(page.getByTestId("slot-a")).toContainText("alpine:3.20");
  });
});
