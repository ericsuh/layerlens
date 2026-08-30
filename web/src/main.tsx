import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";
import { ThemeProvider } from "./theme";

const container = document.getElementById("root");
if (!container) {
  throw new Error("#root container is missing from index.html");
}

/**
 * Defaults per ARCHITECTURE §8.2. Comparison data is content-addressed by
 * image digests, so most keys are immutable and per-query `staleTime` overrides
 * live next to the query that needs them.
 */
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1,
    },
  },
});

createRoot(container).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <App />
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>,
);
