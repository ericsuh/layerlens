import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { DockerImageSummary, DockerListing } from "../api/types";
import { DockerList } from "./DockerList";
import { initialSlotState } from "./slots";

const NO_SOCKET =
  "No Docker socket found at /var/run/docker.sock — the daemon source is unavailable on this server.";

function image(overrides: Partial<DockerImageSummary> = {}): DockerImageSummary {
  return {
    reference: "local/app:dev",
    dockerId: "sha256:1111",
    sizeBytes: 15_000_000,
    platform: "linux/amd64",
    alreadyAnalyzed: false,
    ...overrides,
  };
}

function renderList(listing: DockerListing, slots = initialSlotState) {
  const onPick = vi.fn();
  const onSet = vi.fn();
  const onAnalyze = vi.fn();
  render(
    <DockerList
      listing={listing}
      slots={slots}
      analyzing={new Set()}
      onPick={onPick}
      onSet={onSet}
      onAnalyze={onAnalyze}
    />,
  );
  return { onPick, onSet, onAnalyze };
}

describe("DockerList", () => {
  it("shows the server's own explanation and no action when there is no daemon (state #4)", () => {
    renderList({ available: false, reason: NO_SOCKET, images: [] });

    expect(screen.getByText(NO_SOCKET)).toBeInTheDocument();
    // Nothing the user can do in-app, so nothing that looks like they can.
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("says so when a reachable daemon holds no images (state #5)", () => {
    renderList({ available: true, images: [] });
    expect(screen.getByText("The Docker daemon has no images.")).toBeInTheDocument();
  });

  it("renders an unanalyzed image with an Analyze action and a note", async () => {
    const user = userEvent.setup();
    const { onAnalyze } = renderList({ available: true, images: [image()] });

    const row = screen.getByTestId("docker-row-local/app:dev");
    expect(row).toHaveTextContent("local/app:dev");
    expect(row).toHaveTextContent("linux/amd64");
    expect(row).toHaveTextContent("14.3 MiB");
    expect(row).toHaveTextContent("will be analyzed");
    // No analysis means no image id, so there is nothing a slot could hold.
    expect(row).not.toHaveAttribute("role", "button");

    await user.click(within(row).getByRole("button", { name: "Analyze" }));
    expect(onAnalyze).toHaveBeenCalledWith("local/app:dev");
  });

  it("makes an already-analyzed image a slot target, keyed by its analyzed id", async () => {
    const user = userEvent.setup();
    const { onPick, onSet } = renderList({
      available: true,
      images: [image({ alreadyAnalyzed: true, analyzedId: "sha256:analyzed" })],
    });

    const row = screen.getByTestId("docker-row-local/app:dev");
    expect(within(row).queryByRole("button", { name: "Analyze" })).toBeNull();

    await user.click(within(row).getByRole("button", { name: "Set B" }));
    expect(onSet).toHaveBeenCalledWith("b", "sha256:analyzed");

    await user.click(row);
    expect(onPick).toHaveBeenCalledWith("sha256:analyzed");
  });

  it("marks the row that is already in a slot", () => {
    renderList(
      {
        available: true,
        images: [image({ alreadyAnalyzed: true, analyzedId: "sha256:analyzed" })],
      },
      { a: "sha256:analyzed", b: null, armed: "b" },
    );
    expect(screen.getByTestId("docker-row-local/app:dev")).toHaveAttribute("aria-pressed", "true");
  });

  it("offers a filter once the list is long enough to need one", async () => {
    const user = userEvent.setup();
    renderList({
      available: true,
      images: Array.from({ length: 9 }, (_, index) =>
        image({ reference: `local/app-${String(index)}:dev`, dockerId: `sha256:${String(index)}` }),
      ),
    });

    await user.type(screen.getByRole("searchbox", { name: /filter/i }), "app-3");
    expect(screen.getByTestId("docker-row-local/app-3:dev")).toBeInTheDocument();
    expect(screen.queryByTestId("docker-row-local/app-4:dev")).toBeNull();
  });

  it("disables Analyze while that reference is already being pulled", () => {
    render(
      <DockerList
        listing={{ available: true, images: [image()] }}
        slots={initialSlotState}
        analyzing={new Set(["local/app:dev"])}
        onPick={vi.fn()}
        onSet={vi.fn()}
        onAnalyze={vi.fn()}
      />,
    );
    expect(screen.getByRole("button", { name: "Analyzing…" })).toBeDisabled();
  });
});
