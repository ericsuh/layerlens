import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * The remote-source error paths (DESIGN §9 rows 4, 7, 8, 10).
 *
 * Every one of these runs with **no network and no Docker**: the two states
 * that would otherwise need them are stubbed with `page.route`, and the two
 * that must not reach the server are asserted to have made no request at all.
 * That is the point of the pre-flight verdict — a refused registry never
 * becomes a socket, on either side of the wire.
 */

const DENIED = {
  id: "e2e-pull-1",
  reference: "ghcr.io/private/thing:v1",
  source: "registry",
  state: "error",
  startedAt: "2026-08-29T12:00:00Z",
  bytesDone: 0,
  bytesEstimated: false,
  layersDone: 0,
  layersSkipped: 0,
  error: {
    code: "pull_upstream_denied",
    message:
      "That image was not found, or it requires authentication. layerlens supports anonymous public pulls only.",
  },
};

/** Counts POSTs to /pulls while letting every request through untouched. */
async function countPullPosts(page: Page): Promise<() => number> {
  let posts = 0;
  await page.route("**/api/v1/pulls", async (route) => {
    if (route.request().method() === "POST") {
      posts += 1;
    }
    await route.continue();
  });
  return () => posts;
}

async function openRegistryTab(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("tab", { name: /Registry/ }).click();
  await expect(page.getByTestId("registry-input")).toBeVisible();
}

test.describe("registry input validation", () => {
  test("a reference that does not parse is refused inline (state #7)", async ({ page }) => {
    const posts = await countPullPosts(page);
    await openRegistryTab(page);

    await page.getByTestId("registry-input").fill("not a ref!");

    await expect(page.getByTestId("registry-verdict")).toHaveText("Not a valid image reference");
    await expect(page.getByTestId("registry-submit")).toBeDisabled();
    expect(posts()).toBe(0);
  });

  test("a registry off the allowlist is named, with the allowed ones (state #8)", async ({
    page,
  }) => {
    const posts = await countPullPosts(page);
    await openRegistryTab(page);

    await page.getByTestId("registry-input").fill("evil.example.com/x");

    await expect(page.getByTestId("registry-verdict")).toHaveText(
      "evil.example.com isn’t on the allowlist. Allowed: Docker Hub, GHCR, GCR, ECR, ACR.",
    );
    await expect(page.getByTestId("registry-submit")).toBeDisabled();

    // The whole claim of the pre-flight check: this reference costs no request.
    await page.waitForTimeout(300);
    expect(posts()).toBe(0);
  });

  test("a valid reference resolves its registry and arms the button", async ({ page }) => {
    await openRegistryTab(page);
    await page.getByTestId("registry-input").fill("ghcr.io/org/img:tag");
    await expect(page.getByTestId("registry-verdict")).toContainText("ghcr.io");
    await expect(page.getByTestId("registry-verdict")).toContainText("allowed");
    await expect(page.getByTestId("registry-submit")).toBeEnabled();
  });
});

test.describe("pull failures", () => {
  test("a private or missing image gets the non-leaking message (state #10)", async ({ page }) => {
    let started = false;
    await page.route("**/api/v1/pulls", async (route) => {
      if (route.request().method() === "POST") {
        started = true;
        await route.fulfill({ status: 202, json: DENIED });
        return;
      }
      await route.fulfill({ json: { pulls: started ? [DENIED] : [] } });
    });

    await openRegistryTab(page);
    await page.getByTestId("registry-input").fill("ghcr.io/private/thing:v1");
    await page.getByTestId("registry-submit").click();

    const card = page.getByTestId("pull-ghcr.io/private/thing:v1");
    await expect(card).toBeVisible();
    await expect(card.getByTestId("pull-error")).toContainText(
      "That image is not publicly available",
    );
    await expect(card.getByTestId("pull-error")).toContainText(
      "layerlens supports anonymous public pulls only.",
    );
    await expect(card.getByRole("button", { name: "Retry" })).toBeVisible();
  });
});

test.describe("docker daemon source", () => {
  test("an absent socket explains itself and offers nothing to click (state #4)", async ({
    page,
  }) => {
    const reason =
      "No Docker socket found at /var/run/docker.sock — the daemon source is unavailable on this server.";
    await page.route("**/api/v1/docker/images", async (route) => {
      await route.fulfill({ json: { available: false, reason, images: [] } });
    });

    await page.goto("/");
    const tab = page.getByRole("tab", { name: /Docker daemon/ });
    await expect(tab).toContainText("unavailable");
    await tab.click();

    await expect(page.getByText(reason)).toBeVisible();
    // An unavailable daemon is a fact about the deployment, not an error.
    await expect(page.getByRole("alert")).toHaveCount(0);
  });

  test("a daemon that answers and then fails offers a Retry (state #6)", async ({ page }) => {
    let attempts = 0;
    await page.route("**/api/v1/docker/images", async (route) => {
      attempts += 1;
      await route.fulfill({
        status: 503,
        json: {
          error: { code: "docker_unavailable", message: "The Docker daemon could not be queried." },
        },
      });
    });

    await page.goto("/");
    await page.getByRole("tab", { name: /Docker daemon/ }).click();
    await expect(page.getByRole("alert")).toContainText("The Docker daemon could not be queried.");

    const before = attempts;
    await page.getByRole("button", { name: "Retry" }).click();
    await expect.poll(() => attempts).toBeGreaterThan(before);
  });
});
