import type { LayerInfo } from "../api/types";

/**
 * Client-side mirror of `analyze.CleanInstruction`
 * (`internal/analyze/history.go`). The server already sends a cleaned
 * `instruction`, so this is a display fallback rather than the source of
 * truth — it exists so a layer whose cleaned form is empty (or an
 * `instructionRaw` shown somewhere the server did not clean) still renders
 * without builder decoration. Keep the two in step: the Go table test
 * `TestCleanInstruction` and the Vitest table here cover the same forms.
 */
const NOP_PREFIX = "/bin/sh -c #(nop)";
const SHELL_PREFIX = "/bin/sh -c ";
const RUN_SHELL_PREFIX = `RUN ${SHELL_PREFIX}`;
const BUILDKIT_SUFFIX = "# buildkit";

export function cleanInstruction(raw: string): string {
  let s = raw.trim();

  // BuildKit appends "# buildkit"; strip it before looking at prefixes so
  // "RUN /bin/sh -c npm install # buildkit" reduces cleanly.
  if (s.endsWith(BUILDKIT_SUFFIX)) {
    s = s.slice(0, s.length - BUILDKIT_SUFFIX.length).replace(/[ \t]+$/, "");
  }

  if (s.startsWith(NOP_PREFIX)) {
    // Classic builder metadata instruction: "#(nop)" is followed by the real
    // instruction, sometimes with doubled spaces.
    s = s.slice(NOP_PREFIX.length).trim();
  } else if (s.startsWith(RUN_SHELL_PREFIX)) {
    // BuildKit RUN: keep the verb, drop the shell wrapper.
    s = `RUN ${s.slice(RUN_SHELL_PREFIX.length)}`;
  } else if (s.startsWith(SHELL_PREFIX)) {
    // Classic builder RUN: the config records only the shell invocation, so
    // the verb is not ours to invent.
    s = s.slice(SHELL_PREFIX.length);
  }

  return s.trim();
}

/** The Dockerfile verbs whose leading token we render in 550 weight (DESIGN §5.2). */
const KEYWORDS = new Set([
  "ADD",
  "ARG",
  "CMD",
  "COPY",
  "ENTRYPOINT",
  "ENV",
  "EXPOSE",
  "FROM",
  "HEALTHCHECK",
  "LABEL",
  "MAINTAINER",
  "ONBUILD",
  "RUN",
  "SHELL",
  "STOPSIGNAL",
  "USER",
  "VOLUME",
  "WORKDIR",
]);

export interface DisplayInstruction {
  /** The leading Dockerfile verb, or "" when the text does not start with one. */
  keyword: string;
  /** Everything after the verb — the part that truncates. */
  rest: string;
  /** Verbatim `created_by`, for the popover. */
  raw: string;
  /**
   * True when the history↔layer mapping failed (state #21): the card renders
   * an italic muted "instruction unknown" instead of text it cannot vouch for.
   */
  unknown: boolean;
}

/**
 * Splits a layer's instruction into the parts the card renders. Never invents
 * text: an unmapped layer is reported as unknown rather than shown blank, and
 * a mapped-but-verbless instruction keeps its whole text in `rest`.
 */
export function displayInstruction(
  layer: Pick<LayerInfo, "instruction" | "instructionRaw" | "instructionKnown">,
): DisplayInstruction {
  const raw = layer.instructionRaw;
  const cleaned = layer.instruction !== "" ? layer.instruction : cleanInstruction(raw);

  if (!layer.instructionKnown || cleaned === "") {
    return { keyword: "", rest: "instruction unknown", raw, unknown: true };
  }

  const space = cleaned.indexOf(" ");
  const head = space === -1 ? cleaned : cleaned.slice(0, space);
  if (!KEYWORDS.has(head)) {
    return { keyword: "", rest: cleaned, raw, unknown: false };
  }
  return {
    keyword: head,
    rest: space === -1 ? "" : cleaned.slice(space + 1).trim(),
    raw,
    unknown: false,
  };
}

/**
 * A single-line summary for `title`/aria text. The card itself truncates with
 * CSS (never a substring), so this exists only for the places that need a
 * plain string.
 */
export function instructionLabel(display: DisplayInstruction): string {
  return display.keyword === "" ? display.rest : `${display.keyword} ${display.rest}`.trim();
}
