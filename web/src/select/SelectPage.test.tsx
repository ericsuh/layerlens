import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { GOLDEN_IMAGES } from "../fixtures";
import { renderApp } from "../testing";
import { SelectPage } from "./SelectPage";

function mockImages(body: unknown = GOLDEN_IMAGES, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(body), {
          status,
          headers: { "content-type": "application/json" },
        }),
      ),
    ),
  );
}

async function row(name: string) {
  return await screen.findByTestId(`analyzed-row-${name}`);
}

describe("SelectPage", () => {
  beforeEach(() => {
    mockImages();
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
    mockImages({ error: { code: "internal", message: "The image index is unavailable." } }, 500);
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

  it("offers Docker and Registry as visibly unavailable tabs, not dead buttons", async () => {
    renderApp(<SelectPage />);
    await screen.findByTestId("analyzed-row-example:v1");

    expect(screen.getByRole("tab", { name: /Analyzed/ })).toBeEnabled();
    expect(screen.getByRole("tab", { name: /Docker daemon/, hidden: true })).toBeDisabled();
    expect(screen.getByRole("tab", { name: /Registry/, hidden: true })).toBeDisabled();
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
