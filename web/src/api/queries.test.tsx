import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { stubFetch } from "../testing";
import type { StubCall } from "../testing";
import { isPullActive, PULL_POLL_MS, queryKeys, usePullQuery, usePullsQuery, useStartPull } from "./queries";
import type { PullState, PullStatus } from "./types";

function pull(state: PullState, extra: Partial<PullStatus> = {}): PullStatus {
  return {
    id: "pull-1",
    reference: "ghcr.io/org/img:tag",
    source: "registry",
    state,
    startedAt: "2026-08-29T12:00:00Z",
    bytesDone: 10,
    bytesEstimated: false,
    layersDone: 1,
    layersSkipped: 0,
    ...(state === "done" ? { imageId: "sha256:abc" } : {}),
    ...extra,
  };
}

function harness() {
  // A non-zero gcTime because one of these tests is *about* the cache: the
  // POST seeds ['pull', id] before anything observes it, and at gcTime 0 that
  // write is collected before it can be read back.
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 5_000 } },
  });
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, invalidate, wrapper };
}

/** Long enough for several poll intervals to have fired if polling were live. */
const SETTLE_MS = PULL_POLL_MS * 3;

function pollCount(calls: StubCall[]): number {
  return calls.filter((call) => call.method === "GET").length;
}

describe("pull polling state machine (ARCHITECTURE §9.3)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("polls ['pull', id] while running and stops dead on done", async () => {
    let served = 0;
    const calls = stubFetch({
      "GET /api/v1/pulls/*": () => {
        served += 1;
        return { body: served === 1 ? pull("running") : pull("done") };
      },
    });
    const { wrapper } = harness();

    const { result } = renderHook(() => usePullQuery("pull-1"), { wrapper });
    await waitFor(() => {
      expect(result.current.data?.state).toBe("done");
    });
    const settled = pollCount(calls);

    await new Promise((resolve) => setTimeout(resolve, SETTLE_MS));
    // A terminal pull can never change again, so a further request could only
    // return what the client already has.
    expect(pollCount(calls)).toBe(settled);
  });

  it("invalidates ['images'] exactly once when a pull lands", async () => {
    stubFetch({ "GET /api/v1/pulls/*": { body: pull("done") } });
    const { invalidate, wrapper } = harness();

    const { result } = renderHook(() => usePullQuery("pull-1"), { wrapper });
    await waitFor(() => {
      expect(result.current.data?.state).toBe("done");
    });
    await new Promise((resolve) => setTimeout(resolve, SETTLE_MS));

    const invalidatedKeys = (key: readonly unknown[]) =>
      invalidate.mock.calls.filter(
        ([args]) => JSON.stringify(args?.queryKey) === JSON.stringify(key),
      );
    expect(invalidatedKeys(queryKeys.images())).toHaveLength(1);
    // The daemon listing's `alreadyAnalyzed` flags are about the same records.
    expect(invalidatedKeys(queryKeys.dockerImages())).toHaveLength(1);
  });

  it("does not poll ['pulls'] when nothing is active", async () => {
    const calls = stubFetch({ "GET /api/v1/pulls": { body: { pulls: [pull("cancelled")] } } });
    const { wrapper } = harness();

    const { result } = renderHook(() => usePullsQuery(), { wrapper });
    await waitFor(() => {
      expect(result.current.data?.pulls).toHaveLength(1);
    });
    await new Promise((resolve) => setTimeout(resolve, SETTLE_MS));
    expect(pollCount(calls)).toBe(1);
  });

  it("keeps polling ['pulls'] while one is still running", async () => {
    const calls = stubFetch({ "GET /api/v1/pulls": { body: { pulls: [pull("running")] } } });
    const { wrapper } = harness();

    renderHook(() => usePullsQuery(), { wrapper });
    await waitFor(() => {
      expect(pollCount(calls)).toBeGreaterThan(1);
    });
  });

  it("seeds ['pull', id] from the POST so the card renders without a first GET", async () => {
    const calls = stubFetch({ "POST /api/v1/pulls": { status: 202, body: pull("resolving") } });
    const { client, wrapper } = harness();

    const { result } = renderHook(() => useStartPull(), { wrapper });
    result.current.mutate({ source: "registry", reference: "ghcr.io/org/img:tag" });

    await waitFor(() => {
      expect(client.getQueryData(queryKeys.pull("pull-1"))).toMatchObject({ state: "resolving" });
    });
    expect(calls.filter((call) => call.method === "POST")).toHaveLength(1);
    expect(calls[0]?.body).toEqual({ source: "registry", reference: "ghcr.io/org/img:tag" });
  });
});

describe("isPullActive", () => {
  it.each([
    ["resolving", true],
    ["running", true],
    ["done", false],
    ["error", false],
    ["cancelled", false],
  ] as const)("reports %s as active=%s", (state, expected) => {
    expect(isPullActive(pull(state))).toBe(expected);
  });
});
