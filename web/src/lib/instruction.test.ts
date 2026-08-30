import { describe, expect, it } from "vitest";

import { cleanInstruction, displayInstruction, instructionLabel } from "./instruction";

describe("cleanInstruction", () => {
  // These cases mirror TestCleanInstruction in internal/analyze/history_test.go.
  it.each([
    ["RUN /bin/sh -c npm install # buildkit", "RUN npm install"],
    ["COPY . . # buildkit", "COPY . ."],
    [
      "RUN /bin/sh -c apt-get update && apt-get install -y ffmpeg # buildkit",
      "RUN apt-get update && apt-get install -y ffmpeg",
    ],
    ["/bin/sh -c #(nop) COPY dir:0f2 in /app ", "COPY dir:0f2 in /app"],
    ["/bin/sh -c #(nop)  ENV NODE_VERSION=22", "ENV NODE_VERSION=22"],
    ["/bin/sh -c apt-get update", "apt-get update"],
    ["WORKDIR /app", "WORKDIR /app"],
    ["", ""],
  ])("cleans %j", (raw, expected) => {
    expect(cleanInstruction(raw)).toBe(expected);
  });

  it("does not strip a '# buildkit' that is not a suffix", () => {
    expect(cleanInstruction("RUN echo '# buildkit' > /tmp/x")).toBe("RUN echo '# buildkit' > /tmp/x");
  });
});

describe("displayInstruction", () => {
  it("splits the leading Dockerfile keyword from the rest", () => {
    const display = displayInstruction({
      instruction: "RUN npm install",
      instructionRaw: "RUN /bin/sh -c npm install # buildkit",
      instructionKnown: true,
    });
    expect(display).toMatchObject({ keyword: "RUN", rest: "npm install", unknown: false });
  });

  it("preserves the raw text for the popover", () => {
    const raw = "RUN /bin/sh -c npm install # buildkit";
    expect(
      displayInstruction({ instruction: "RUN npm install", instructionRaw: raw, instructionKnown: true })
        .raw,
    ).toBe(raw);
  });

  it("falls back to cleaning the raw text when the server sent no cleaned form", () => {
    const display = displayInstruction({
      instruction: "",
      instructionRaw: "COPY . . # buildkit",
      instructionKnown: true,
    });
    expect(instructionLabel(display)).toBe("COPY . .");
  });

  it("takes the italic-unknown path when the mapping failed (state #21)", () => {
    const display = displayInstruction({
      instruction: "",
      instructionRaw: "",
      instructionKnown: false,
    });
    expect(display.unknown).toBe(true);
    expect(display.rest).toBe("instruction unknown");
    expect(display.keyword).toBe("");
  });

  it("never invents a keyword for text that does not start with one", () => {
    const display = displayInstruction({
      instruction: "apt-get update",
      instructionRaw: "/bin/sh -c apt-get update",
      instructionKnown: true,
    });
    expect(display.keyword).toBe("");
    expect(display.rest).toBe("apt-get update");
  });

  it("handles a keyword with no arguments", () => {
    const display = displayInstruction({
      instruction: "USER",
      instructionRaw: "USER",
      instructionKnown: true,
    });
    expect(display).toMatchObject({ keyword: "USER", rest: "" });
    expect(instructionLabel(display)).toBe("USER");
  });
});
