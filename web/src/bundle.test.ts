import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

// The artifact `mise run build-web` produces and `go:embed` compiles into the
// binary. Everything else in this suite renders from source, so this is the
// only test that can catch a build-flag regression: a wrong --format, a bad
// --target, or a define that leaves React in development mode.
const bundlePath = resolve(
  dirname(fileURLToPath(import.meta.url)),
  "../../internal/webui/dist/app.js",
);

describe("built bundle", () => {
  if (!existsSync(bundlePath)) {
    it.fails("requires the built bundle", () => {
      throw new Error(`missing ${bundlePath}; run \`mise run build-web\` first`);
    });
    return;
  }

  const source = readFileSync(bundlePath, "utf8");

  it("is an IIFE, not an ES module", () => {
    // DECISIONS records --format=iife so the bundle runs in environments that
    // do not execute <script type="module">. An ESM build would carry
    // top-level import/export statements.
    expect(source).not.toMatch(/^\s*(import|export)[\s{*]/m);
  });

  it("is minified and ships no source map", () => {
    expect(source).not.toMatch(/\/\/# sourceMappingURL=/);
    expect(existsSync(`${bundlePath}.map`)).toBe(false);
    // The unminified bundle is ~1.19 MB; the minified one is ~220 KB.
    expect(source.length).toBeLessThan(600_000);
  });

  it("is built for production", () => {
    // React's development build ships these warnings; the production one does not.
    expect(source).not.toContain("Warning: ReactDOM.render");
    expect(source).not.toContain("react-dom.development.js");
  });

  it("mounts the application into #root", async () => {
    document.body.innerHTML = '<div id="root"></div>';
    const root = document.getElementById("root");
    expect(root).not.toBeNull();
    expect(root?.innerHTML).toBe("");

    // Evaluate exactly what a browser would run, against the jsdom globals.
    // The Function constructor is the point of the test: importing the source
    // instead would prove nothing about the emitted artifact. The input is a
    // file this repository just built, not user data.
    // eslint-disable-next-line @typescript-eslint/no-implied-eval, @typescript-eslint/no-unsafe-call
    new Function(source)();

    await expect
      .poll(() => root?.textContent ?? "", { timeout: 5000 })
      .toContain("layerlens");

    // The shipped Content-Security-Policy is `default-src 'none'` with
    // `script-src 'self'` and `style-src 'self'`, which forbids inline
    // <script> and <style> *elements*. jsdom does not enforce CSP, so assert
    // the rendered tree stays inside it. Style attributes are deliberately
    // not asserted against: `style-src-attr 'unsafe-inline'` permits them
    // (see internal/webui/webui.go and DECISIONS, "Phase 006"), because
    // portalled overlays and the measured layer diagram need them.
    expect(document.querySelectorAll("script:not([src])")).toHaveLength(0);
    expect(document.querySelectorAll("style")).toHaveLength(0);
  });

  it("ships a shell that needs no inline script or style", () => {
    const shell = readFileSync(resolve(dirname(bundlePath), "index.html"), "utf8");
    expect(shell).toMatch(/<script[^>]+src=/);
    expect(shell).not.toMatch(/<script(?![^>]*\bsrc=)/);
    expect(shell).not.toMatch(/<style[\s>]/);
    expect(shell).not.toMatch(/\sstyle=/);
    // Every subresource is same-origin: the CSP names no external host.
    expect(shell).not.toMatch(/(src|href)="https?:/);
  });
});
