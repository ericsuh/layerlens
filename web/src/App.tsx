import { Link, Route, Switch, useSearch } from "wouter";

import { useLayerGraphQuery } from "./api/queries";
import { ImageChip } from "./components/identity";
import { EmptyPanel } from "./components/states";
import { TooltipProvider } from "./components/ui/tooltip";
import { ComparePage } from "./compare/ComparePage";
import { parseCompareSearch } from "./lib/urlstate";
import { SelectPage } from "./select/SelectPage";
import { ThemeToggle } from "./theme";

/**
 * The header's pair of identity chips. They read the same query the compare
 * page reads, so they cost no extra request and can never disagree with the
 * diagram about which image is A.
 */
function HeaderImages() {
  const search = useSearch();
  const { left, right } = parseCompareSearch(search);
  const query = useLayerGraphQuery(left, right);
  const graph = query.data;

  if (graph === undefined) {
    return null;
  }
  return (
    <div className="flex min-w-0 items-center gap-2">
      <ImageChip side="a" image={graph.left} />
      <span className="text-text-muted text-[12px]">vs</span>
      <ImageChip side="b" image={graph.right} />
      <Link href="/" className="ll-link ml-2">
        Change images
      </Link>
    </div>
  );
}

export function App() {
  return (
    <TooltipProvider delayDuration={200}>
      <div className="flex h-full flex-col">
        <header className="border-border bg-surface flex h-14 flex-none items-center gap-4 border-b px-8">
          <Link href="/" className="flex items-center gap-2 text-[16px] font-[650] tracking-[-0.01em]">
            <span
              className="from-image-a to-image-b h-5 w-5 flex-none rounded-[5px] bg-gradient-to-br"
              aria-hidden="true"
            />
            <span data-testid="app-title">layerlens</span>
          </Link>
          <Route path="/compare">
            <HeaderImages />
          </Route>
          <div className="flex-1" />
          <ThemeToggle />
        </header>

        <main className="min-h-0 flex-1 overflow-auto">
          <Switch>
            <Route path="/" component={SelectPage} />
            <Route path="/compare" component={ComparePage} />
            <Route>
              <EmptyPanel
                title="Page not found"
                detail="That address does not match any layerlens view."
                action={{ label: "Go to image selection", href: "/" }}
              />
            </Route>
          </Switch>
        </main>
      </div>
    </TooltipProvider>
  );
}
