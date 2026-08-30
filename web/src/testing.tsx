import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { RenderResult } from "@testing-library/react";
import type { ReactElement } from "react";
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
  options: { path?: string } = {},
): RenderResult & { history: string[]; navigate: (to: string) => void } {
  const { hook, searchHook, navigate, history } = memoryLocation({
    path: options.path ?? "/",
    record: true,
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
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
