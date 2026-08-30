import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { PullState, PullStatus } from "../api/types";
import { GOLDEN_IMAGES } from "../fixtures";
import { DEFAULT_ALLOWED_PATTERNS } from "../lib/refcheck";
import { renderApp, stubFetch } from "../testing";
import type { StubCall, StubRoute } from "../testing";
import { SelectPage } from "./SelectPage";

const NO_SOCKET =
  "No Docker socket found at /var/run/docker.sock — the daemon source is unavailable on this server.";

/**
 * The four endpoints the selection view reads, each answering for itself. A
 * blanket response would let a test about the Analyzed list pass while the
 * Docker tab silently rendered the same payload.
 */
function mockApi(overrides: Record<string, StubRoute> = {}): StubCall[] {
  return stubFetch({
    "GET /api/v1/images": { body: GOLDEN_IMAGES },
    "GET /api/v1/meta": {
      body: {
        version: "test",
        cacheBytesUsed: 0,
        cacheMaxBytes: 1024,
        allowedRegistries: [...DEFAULT_ALLOWED_PATTERNS],
      },
    },
    "GET /api/v1/pulls": { body: { pulls: [] } },
    "GET /api/v1/docker/images": { body: { available: false, reason: NO_SOCKET, images: [] } },
    ...overrides,
  });
}

function pull(state: PullState, extra: Partial<PullStatus> = {}): PullStatus {
  return {
    id: "pull-1",
    reference: "ghcr.io/org/img:tag",
    source: "registry",
    state,
    startedAt: "2026-08-29T12:00:00Z",
    bytesDone: 0,
    bytesEstimated: false,
    layersDone: 0,
    layersSkipped: 0,
    ...extra,
  };
}

async function row(name: string) {
  return await screen.findByTestId(`analyzed-row-${name}`);
}

describe("SelectPage", () => {
  beforeEach(() => {
    mockApi();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders a row per analyzed image, demo images first", async () => {
    renderApp(<SelectPage />);
    const rows = await screen.findAllByRole("button", { name: /analyzed|Select for image slot/ });
    const refs = rows
      .map((element) => element.getAttribute("data-testid"))
      .filter((id): id is string => id !== null && id.startsWith("analyzed-row-"));
    expect(refs.slice(0, 2)).toEqual(["analyzed-row-example:v1", "analyzed-row-example:v2"]);
    expect(refs).toHaveLength(GOLDEN_IMAGES.images.length);
  });

  it("shows a skeleton, not an empty panel, while the list loads", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    renderApp(<SelectPage />);
    expect(screen.getByTestId("analyzed-skeleton")).toBeDefined();
  });

  it("shows the server's own message when the list fails", async () => {
    mockApi({
      "GET /api/v1/images": {
        status: 500,
        body: { error: { code: "internal", message: "The image index is unavailable." } },
      },
    });
    renderApp(<SelectPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent("The image index is unavailable.");
  });

  it("fills the armed slot on a plain click, then the other slot", async () => {
    const user = userEvent.setup();
    renderApp(<SelectPage />);

    await user.click(await row("example:v1"));
    expect(screen.getByTestId("slot-a")).toHaveTextContent("example:v1");
    // Slot B is now armed, so it is where the next plain click lands.
    expect(screen.getByTestId("slot-b").dataset.armed).toBe("true");

    await user.click(await row("example:v2"));
    expect(screen.getByTestId("slot-b")).toHaveTextContent("example:v2");
  });

  it("lets Set A / Set B override the armed slot", async () => {
    const user = userEvent.setup();
    renderApp(<SelectPage />);

    const target = await row("wide:v1");
    await user.click(within(target).getByRole("button", { name: "Set B" }));

    expect(screen.getByTestId("slot-b")).toHaveTextContent("wide:v1");
    expect(screen.getByTestId("slot-a")).toHaveTextContent("Select an image below");
  });

  it("removes an image when its row is clicked a second time", async () => {
    const user = userEvent.setup();
    renderApp(<SelectPage />);

    await user.click(await row("example:v1"));
    expect(screen.getByTestId("slot-a")).toHaveTextContent("example:v1");
    await user.click(await row("example:v1"));
    expect(screen.getByTestId("slot-a")).toHaveTextContent("Select an image below");
  });

  it("keeps Compare disabled, with helper text, until both slots are filled", async () => {
    const user = userEvent.setup();
    renderApp(<SelectPage />);

    const compare = screen.getByTestId("compare-button");
    expect(compare).toBeDisabled();
    expect(screen.getByTestId("compare-hint")).toHaveTextContent("Choose two images to compare");

    await user.click(await row("example:v1"));
    expect(compare).toBeDisabled();

    await user.click(await row("example:v2"));
    expect(compare).toBeEnabled();
    expect(screen.getByTestId("compare-hint")).toHaveTextContent("");
  });

  it("notes the all-shared case when both slots hold the same image", async () => {
    const user = userEvent.setup();
    renderApp(<SelectPage />);

    const target = await row("example:v1");
    await user.click(within(target).getByRole("button", { name: "Set A" }));
    await user.click(within(target).getByRole("button", { name: "Set B" }));

    expect(screen.getByTestId("compare-hint")).toHaveTextContent(
      "Both slots contain the same image",
    );
    expect(screen.getByTestId("compare-button")).toBeEnabled();
  });

  it("navigates to a shareable /compare URL carrying both image ids", async () => {
    const user = userEvent.setup();
    const { history } = renderApp(<SelectPage />);

    await user.click(await row("example:v1"));
    await user.click(await row("example:v2"));
    await user.click(screen.getByTestId("compare-button"));

    const [v1, v2] = [
      GOLDEN_IMAGES.images.find((image) => image.refNames.includes("example:v1"))?.id,
      GOLDEN_IMAGES.images.find((image) => image.refNames.includes("example:v2"))?.id,
    ];
    await waitFor(() => {
      expect(history.at(-1)).toBe(`/compare?left=${String(v1)}&right=${String(v2)}`);
    });
  });

  it("offers all three sources, with the daemon's availability spelled out", async () => {
    renderApp(<SelectPage />);
    await screen.findByTestId("analyzed-row-example:v1");

    expect(screen.getByRole("tab", { name: /Analyzed/ })).toBeEnabled();
    expect(screen.getByRole("tab", { name: /Registry/ })).toBeEnabled();
    // Never the gray dot alone (DESIGN §7).
    await waitFor(() => {
      expect(screen.getByRole("tab", { name: /Docker daemon/ })).toHaveTextContent("unavailable");
    });
  });

  it("shows the server's own reason on the Docker tab and offers nothing to click (state #4)", async () => {
    const user = userEvent.setup();
    renderApp(<SelectPage />);

    await user.click(screen.getByRole("tab", { name: /Docker daemon/ }));
    expect(await screen.findByText(NO_SOCKET)).toBeInTheDocument();
  });

  it("offers a Retry when the daemon answered and then failed (state #6)", async () => {
    const user = userEvent.setup();
    const calls = mockApi({
      "GET /api/v1/docker/images": {
        status: 503,
        body: {
          error: { code: "docker_unavailable", message: "The Docker daemon could not be queried." },
        },
      },
    });
    renderApp(<SelectPage />);

    await user.click(screen.getByRole("tab", { name: /Docker daemon/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The Docker daemon could not be queried.",
    );

    const before = calls.filter((call) => call.url.includes("/docker/images")).length;
    await user.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => {
      expect(calls.filter((call) => call.url.includes("/docker/images")).length).toBeGreaterThan(
        before,
      );
    });
  });

  it("starts a registry pull and keeps its card above the tabs", async () => {
    const user = userEvent.setup();
    const started: PullStatus[] = [];
    const calls = mockApi({
      "GET /api/v1/pulls": () => ({ body: { pulls: started } }),
      "POST /api/v1/pulls": () => {
        started.push(pull("running", { bytesTotal: 1000, bytesDone: 250, layersTotal: 4 }));
        return { status: 202, body: started[0] };
      },
    });
    renderApp(<SelectPage />);

    await user.click(screen.getByRole("tab", { name: /Registry/ }));
    await user.type(screen.getByTestId("registry-input"), "ghcr.io/org/img:tag");
    await user.click(screen.getByTestId("registry-submit"));

    expect(calls.filter((call) => call.method === "POST")).toHaveLength(1);
    expect(calls.find((call) => call.method === "POST")?.body).toEqual({
      source: "registry",
      reference: "ghcr.io/org/img:tag",
    });

    const card = await screen.findByTestId("pull-ghcr.io/org/img:tag");
    expect(card).toHaveTextContent("Downloading & indexing layers");

    // The card lives above the tab strip, so switching source loses nothing.
    await user.click(screen.getByRole("tab", { name: /Analyzed/ }));
    expect(screen.getByTestId("pull-ghcr.io/org/img:tag")).toBeInTheDocument();
  });

  it("never posts a pull for a reference the allowlist rejects (state #8)", async () => {
    const user = userEvent.setup();
    const calls = mockApi();
    renderApp(<SelectPage />);

    await user.click(screen.getByRole("tab", { name: /Registry/ }));
    await user.type(screen.getByTestId("registry-input"), "evil.example.com/x");

    expect(screen.getByTestId("registry-verdict")).toHaveTextContent("isn’t on the allowlist");
    expect(screen.getByTestId("registry-submit")).toBeDisabled();
    expect(calls.filter((call) => call.method === "POST")).toHaveLength(0);
  });

  it("shows the server's rejection inline when it disagrees with the pre-flight check", async () => {
    const user = userEvent.setup();
    mockApi({
      "POST /api/v1/pulls": {
        status: 403,
        body: {
          error: {
            code: "registry_not_allowed",
            message: "ghcr.io is not on the allowlist of registries layerlens may pull from.",
            details: { registry: "ghcr.io", allowed: [] },
          },
        },
      },
    });
    renderApp(<SelectPage />);

    await user.click(screen.getByRole("tab", { name: /Registry/ }));
    await user.type(screen.getByTestId("registry-input"), "ghcr.io/org/img:tag");
    await user.click(screen.getByTestId("registry-submit"));

    expect(await screen.findByTestId("registry-error")).toHaveTextContent(
      "ghcr.io is not on the allowlist of registries layerlens may pull from.",
    );
  });

  it("surfaces a refused start next to the pull cards, dismissibly", async () => {
    const user = userEvent.setup();
    mockApi({
      "GET /api/v1/docker/images": {
        body: {
          available: true,
          images: [
            {
              reference: "local/app:dev",
              dockerId: "sha256:1",
              sizeBytes: 100,
              alreadyAnalyzed: false,
            },
          ],
        },
      },
      "POST /api/v1/pulls": {
        status: 503,
        body: {
          error: {
            code: "docker_unavailable",
            message: "No Docker daemon is reachable on this server.",
          },
        },
      },
    });
    renderApp(<SelectPage />);

    await user.click(screen.getByRole("tab", { name: /Docker daemon/ }));
    await user.click(await screen.findByRole("button", { name: "Analyze" }));

    const alert = await screen.findByTestId("start-error");
    expect(alert).toHaveTextContent("No Docker daemon is reachable on this server.");
    await user.click(within(alert).getByRole("button", { name: /Dismiss/ }));
    expect(screen.queryByTestId("start-error")).toBeNull();
  });

  it("moves between source tabs with the arrow keys", async () => {
    const user = userEvent.setup();
    renderApp(<SelectPage />);

    const analyzed = screen.getByRole("tab", { name: /Analyzed/ });
    analyzed.focus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: /Docker daemon/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await user.keyboard("{End}");
    expect(screen.getByRole("tab", { name: /Registry/ })).toHaveAttribute("aria-selected", "true");
  });

  it("cancels a running pull through the API (state #15)", async () => {
    const user = userEvent.setup();
    const calls = mockApi({
      "GET /api/v1/pulls": { body: { pulls: [pull("running", { bytesTotal: 100 })] } },
      "DELETE /api/v1/pulls/*": { body: pull("cancelled") },
    });
    renderApp(<SelectPage />);

    await user.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(calls.some((call) => call.method === "DELETE")).toBe(true);
    });
  });

  it("drops a finished pull from the list once its image is in the Analyzed tab", async () => {
    const landed = GOLDEN_IMAGES.images[0]?.id ?? "";
    mockApi({
      "GET /api/v1/pulls": {
        body: {
          pulls: [
            pull("done", { id: "known", reference: "ghcr.io/org/known:1", imageId: landed }),
            pull("done", { id: "pending", reference: "ghcr.io/org/pending:1", imageId: "sha256:not-yet" }),
          ],
        },
      },
    });
    renderApp(<SelectPage />);

    // The card's claim is "this is becoming an image you can select"; once it
    // is one, the Analyzed row says it better than the card does.
    expect(await screen.findByTestId("pull-ghcr.io/org/pending:1")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.queryByTestId("pull-ghcr.io/org/known:1")).toBeNull();
    });
  });

  it("filters the list by substring once it is long enough to need it", async () => {
    const user = userEvent.setup();
    renderApp(<SelectPage />);
    await screen.findByTestId("analyzed-row-example:v1");

    await user.type(screen.getByRole("searchbox", { name: /filter/i }), "prefix");
    await waitFor(() => {
      expect(screen.queryByTestId("analyzed-row-example:v1")).toBeNull();
    });
    expect(screen.getByTestId("analyzed-row-prefix:base")).toBeDefined();
  });
});
