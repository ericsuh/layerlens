import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { PullState, PullStatus } from "../api/types";
import {
  estimateRate,
  ETA_MIN_SAMPLE_MS,
  pullMilestone,
  pullPercent,
  pullPhase,
  PullProgressCard,
} from "./PullProgress";

function pull(state: PullState, extra: Partial<PullStatus> = {}): PullStatus {
  return {
    id: "pull-1",
    reference: "ghcr.io/org/img:tag",
    source: "registry",
    state,
    startedAt: "2026-08-29T12:00:00Z",
    bytesTotal: 1000,
    bytesDone: 500,
    bytesEstimated: false,
    layersTotal: 4,
    layersDone: 2,
    layersSkipped: 0,
    ...extra,
  };
}

function stepStates(): Record<string, string | null> {
  const steps = screen.getByTestId("pull-steps").querySelectorAll("li");
  const out: Record<string, string | null> = {};
  for (const step of steps) {
    out[step.getAttribute("data-step") ?? ""] = step.getAttribute("data-state");
  }
  return out;
}

describe("pullPhase", () => {
  it("puts a resolving pull in phase 1", () => {
    expect(pullPhase(pull("resolving"))).toBe("resolving");
  });

  it("puts a running pull in phase 2 while layers remain", () => {
    expect(pullPhase(pull("running"))).toBe("downloading");
  });

  it("moves to finalizing once every layer is accounted for", () => {
    expect(pullPhase(pull("running", { layersDone: 4 }))).toBe("finalizing");
  });

  it("stays in phase 2 while the layer count is still unknown", () => {
    const unknown = pull("running");
    delete unknown.layersTotal;
    expect(pullPhase(unknown)).toBe("downloading");
  });

  it("leaves a pull that died before the manifest in phase 1", () => {
    const early = pull("error");
    delete early.layersTotal;
    expect(pullPhase(early)).toBe("resolving");
    const cancelled = pull("cancelled");
    delete cancelled.layersTotal;
    expect(pullPhase(cancelled)).toBe("resolving");
  });

  it("keeps the phase a cancelled pull actually reached", () => {
    expect(pullPhase(pull("cancelled", { layersDone: 2, layersTotal: 4 }))).toBe("downloading");
  });
});

describe("PullProgressCard", () => {
  it("renders the resolving phase with no byte claims", () => {
    render(<PullProgressCard pull={pull("resolving")} />);
    expect(stepStates()).toEqual({ resolving: "active", downloading: "todo", finalizing: "todo" });
    expect(screen.queryByTestId("pull-numbers")).toBeNull();
  });

  it("renders determinate progress with bytes, layers, throughput and a soft ETA", () => {
    render(
      <PullProgressCard
        pull={pull("running", { bytesTotal: 200_000_000, bytesDone: 50_000_000 })}
        rate={{ bytesPerSecond: 40_054_784, etaSeconds: 245 }}
      />,
    );
    expect(stepStates().downloading).toBe("active");
    const numbers = screen.getByTestId("pull-numbers");
    expect(numbers).toHaveTextContent("47.7 MiB of 191 MiB");
    expect(numbers).toHaveTextContent("2 of 4 layers");
    expect(numbers).toHaveTextContent("38.2 MiB/s");
    expect(numbers).toHaveTextContent("about 4 min left");
    expect(screen.getByTestId("pull-bar")).toHaveAttribute("aria-valuenow", "25");
  });

  it("labels a daemon pull's totals as the estimate they are", () => {
    render(<PullProgressCard pull={pull("running", { source: "docker", bytesEstimated: true })} />);
    expect(screen.getByTestId("pull-numbers")).toHaveTextContent("500 B of about 1000 B");
    expect(screen.getByTestId("pull-numbers")).toHaveTextContent("estimate");
  });

  it("says how many layers were free", () => {
    render(<PullProgressCard pull={pull("running", { layersSkipped: 3 })} />);
    expect(screen.getByTestId("pull-skipped")).toHaveTextContent("3 layers already analyzed");
  });

  it("shows the finalizing phase once every layer has landed", () => {
    render(<PullProgressCard pull={pull("running", { layersDone: 4 })} />);
    expect(stepStates()).toEqual({
      resolving: "done",
      downloading: "done",
      finalizing: "active",
    });
  });

  it("reports a finished pull as complete", () => {
    render(<PullProgressCard pull={pull("done", { imageId: "sha256:abc" })} />);
    expect(screen.getByTestId("pull-done")).toBeInTheDocument();
    expect(stepStates().finalizing).toBe("done");
  });

  it("shows the server's own error sentence under a heading, with Retry (state #10)", async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();
    render(
      <PullProgressCard
        pull={pull("error", {
          error: {
            code: "pull_upstream_denied",
            message:
              "That image was not found, or it requires authentication. layerlens supports anonymous public pulls only.",
          },
        })}
        onRetry={onRetry}
      />,
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("That image is not publicly available");
    expect(alert).toHaveTextContent("layerlens supports anonymous public pulls only.");

    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it("offers Cancel only while the pull can still be cancelled, and cancels (state #15)", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    const { rerender } = render(<PullProgressCard pull={pull("running")} onCancel={onCancel} />);

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledOnce();

    rerender(<PullProgressCard pull={pull("cancelled")} onCancel={onCancel} />);
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
    expect(screen.getByTestId("pull-cancelled")).toHaveTextContent("Pull cancelled.");
  });

  it("lets a terminal card be dismissed but never an active one", async () => {
    const user = userEvent.setup();
    const onDismiss = vi.fn();
    const { rerender } = render(<PullProgressCard pull={pull("running")} onDismiss={onDismiss} />);
    expect(screen.queryByRole("button", { name: /Dismiss/ })).toBeNull();

    rerender(<PullProgressCard pull={pull("error", { error: { code: "pull_failed", message: "nope" } })} onDismiss={onDismiss} />);
    await user.click(screen.getByRole("button", { name: /Dismiss/ }));
    expect(onDismiss).toHaveBeenCalledOnce();
  });
});

describe("pullMilestone", () => {
  it("announces at quarter marks only, so a poll is not an event", () => {
    expect(pullMilestone(pull("running", { bytesDone: 100, bytesTotal: 1000 }))).toBe("");
    expect(pullMilestone(pull("running", { bytesDone: 260, bytesTotal: 1000 }))).toContain("25 percent");
    expect(pullMilestone(pull("running", { bytesDone: 490, bytesTotal: 1000 }))).toContain("25 percent");
    expect(pullMilestone(pull("running", { bytesDone: 510, bytesTotal: 1000 }))).toContain("50 percent");
  });

  it("announces the terminal states as themselves", () => {
    expect(pullMilestone(pull("done"))).toContain("analyzed");
    expect(pullMilestone(pull("error"))).toContain("failed");
    expect(pullMilestone(pull("cancelled"))).toContain("cancelled");
  });
});

describe("pullPercent", () => {
  it("is null until the manifest gives a total", () => {
    const unknown = pull("running");
    delete unknown.bytesTotal;
    expect(pullPercent(unknown)).toBeNull();
  });
});

describe("estimateRate", () => {
  it("says nothing from a single sample", () => {
    expect(estimateRate([{ at: 0, bytes: 0 }])).toEqual({ bytesPerSecond: null, etaSeconds: null });
  });

  it("reports throughput but withholds an ETA until the samples are old enough", () => {
    const early = estimateRate(
      [
        { at: 0, bytes: 0 },
        { at: 1000, bytes: 1_000_000 },
      ],
      10_000_000,
    );
    expect(early.bytesPerSecond).toBe(1_000_000);
    expect(early.etaSeconds).toBeNull();
  });

  it("estimates the remaining time once it has ≥5 s of samples", () => {
    const rate = estimateRate(
      [
        { at: 0, bytes: 0 },
        { at: ETA_MIN_SAMPLE_MS, bytes: 5_000_000 },
      ],
      10_000_000,
    );
    expect(rate.bytesPerSecond).toBe(1_000_000);
    expect(rate.etaSeconds).toBe(5);
  });
});
