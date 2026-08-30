import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { RenderResult } from "@testing-library/react";
import type { ReactElement } from "react";
import { vi } from "vitest";
import { Router } from "wouter";
import { memoryLocation } from "wouter/memory-location";

import { ThemeProvider } from "./theme";
import { TooltipProvider } from "./components/ui/tooltip";

/**
 * Renders a component inside the providers the app actually mounts, over an
 * in-memory location so navigation assertions do not touch window.history.
 */
export function renderApp(
  ui: ReactElement,
  options: {
    path?: string;
    /**
     * `gcTime` defaults to 0 so suites cannot leak cache between tests. Raise
     * it for the few tests that are *about* the cache — the diff tree's
     * depth=2 prefetch writes a directory's page before anything observes it,
     * and at gcTime 0 that write is collected before it can be read.
     */
    gcTime?: number;
  } = {},
): RenderResult & { history: string[]; navigate: (to: string) => void } {
  const { hook, searchHook, navigate, history } = memoryLocation({
    path: options.path ?? "/",
    record: true,
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: options.gcTime ?? 0 } },
  });
  const result = render(
    <QueryClientProvider client={client}>
      <ThemeProvider>
        <TooltipProvider>
          <Router hook={hook} searchHook={searchHook}>
            {ui}
          </Router>
        </TooltipProvider>
      </ThemeProvider>
    </QueryClientProvider>,
  );
  return { ...result, history, navigate };
}

export interface StubCall {
  method: string;
  url: string;
  /** The parsed JSON body of a POST, or undefined for a request without one. */
  body: unknown;
}

export interface StubReply {
  status?: number;
  body?: unknown;
}

export type StubRoute = StubReply | ((call: StubCall) => StubReply);

/**
 * Installs a `fetch` that answers a routing table keyed `"METHOD /path"`, with
 * a trailing `*` for a prefix match (`"DELETE /api/v1/pulls/*"`). The returned
 * array records every call, which is how a test asserts that a request was
 * *not* made — the point of the allowlist pre-flight.
 *
 * Routed rather than one blanket response because the selection view now reads
 * four endpoints at once, and handing all of them the same payload would make
 * a test pass for reasons that have nothing to do with what it names.
 */
export function stubFetch(routes: Record<string, StubRoute>): StubCall[] {
  const calls: StubCall[] = [];
  const keys = Object.keys(routes).sort((a, b) => b.length - a.length);

  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url =
        typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const method = (init?.method ?? "GET").toUpperCase();
      const path = url.split("?")[0] ?? url;
      const call: StubCall = {
        method,
        url,
        body: typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined,
      };
      calls.push(call);

      const key = keys.find((candidate) => {
        const [routeMethod, pattern = ""] = candidate.split(" ");
        if (routeMethod !== method) {
          return false;
        }
        return pattern.endsWith("*") ? path.startsWith(pattern.slice(0, -1)) : pattern === path;
      });
      const route = key === undefined ? undefined : routes[key];
      const reply: StubReply =
        route === undefined
          ? { status: 404, body: { error: { code: "not_found", message: `no stub for ${method} ${path}` } } }
          : typeof route === "function"
            ? route(call)
            : route;

      return Promise.resolve(
        new Response(JSON.stringify(reply.body ?? null), {
          status: reply.status ?? 200,
          headers: { "content-type": "application/json" },
        }),
      );
    }),
  );

  return calls;
}
