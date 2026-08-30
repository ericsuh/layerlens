import { useEffect, useRef, useState } from "react";

import type { PullStatus } from "../api/types";
import { ImageRefText } from "../components/identity";
import { formatBytes, formatSoftEta, formatThroughput } from "../lib/format";

/**
 * The phased determinate progress card (DESIGN §4.4).
 *
 * Rendering is pure — everything it shows comes from a `PullStatus` and a rate
 * estimate passed in — so every state in the §9 table can be asserted from a
 * fixture without a server, a timer or a query client. `LivePullProgress` at
 * the bottom is the thin wrapper that samples throughput over time.
 */

export type PullPhase = "resolving" | "downloading" | "finalizing";

interface PhaseDef {
  id: PullPhase;
  label: string;
  /** Only the middle phase has knowable totals; the other two are brief. */
  determinate: boolean;
}

export const PULL_PHASES: readonly PhaseDef[] = [
  { id: "resolving", label: "Resolving manifest", determinate: false },
  { id: "downloading", label: "Downloading & indexing layers", determinate: true },
  { id: "finalizing", label: "Finalizing analysis", determinate: false },
];

/**
 * Which phase a status is in.
 *
 * The manifest is what makes phase 2 determinate, so `resolving` is phase 1 by
 * definition. The hand-off from downloading to finalizing is the only inferred
 * one: the server keeps reporting `running` while it squashes and writes the
 * index, and the honest signal that the transfer itself is over is every layer
 * being accounted for.
 */
export function pullPhase(pull: PullStatus): PullPhase {
  if (pull.state === "resolving") {
    return "resolving";
  }
  if (pull.state === "done") {
    return "finalizing";
  }
  // Running, cancelled or failed: the manifest is what makes the transfer
  // knowable, so a pull with no layer count never left phase 1 — and a card
  // that showed two green steps for a pull that died on its first request
  // would be claiming work that never happened.
  if (pull.layersTotal === undefined) {
    return pull.state === "running" ? "downloading" : "resolving";
  }
  return pull.layersDone >= pull.layersTotal ? "finalizing" : "downloading";
}

/** Percent of the transfer, or null when the total is not known yet. */
export function pullPercent(pull: PullStatus): number | null {
  if (pull.bytesTotal === undefined || pull.bytesTotal <= 0) {
    return null;
  }
  return Math.min(100, Math.max(0, Math.round((pull.bytesDone / pull.bytesTotal) * 100)));
}

/**
 * The DESIGN §7 live-region text: milestones at 25/50/75/done, not a running
 * commentary. Deriving it from the bucket rather than the exact percent is
 * what keeps a screen reader from announcing every poll.
 */
export function pullMilestone(pull: PullStatus): string {
  switch (pull.state) {
    case "done":
      return `${pull.reference} analyzed.`;
    case "error":
      return `Pull of ${pull.reference} failed.`;
    case "cancelled":
      return `Pull of ${pull.reference} cancelled.`;
    default: {
      const percent = pullPercent(pull);
      if (percent === null) {
        return "";
      }
      const bucket = Math.floor(percent / 25) * 25;
      return bucket === 0 ? "" : `${String(bucket)} percent of ${pull.reference} downloaded.`;
    }
  }
}

/** DESIGN §9 rows 9–13: a heading for the code, the server's own sentence under it. */
const ERROR_HEADINGS: Record<string, string> = {
  pull_upstream_denied: "That image is not publicly available",
  pull_rate_limited: "The registry is rate limiting this server",
  cache_full: "That image does not fit in the cache",
  docker_unavailable: "The Docker daemon is not reachable",
  pull_failed: "That image could not be analyzed",
  // Structural bounds on work, and the admission control in front of them
  // (ARCHITECTURE §6.1). Both are recoverable by the user, so they get their
  // own heading rather than the generic one.
  pull_too_large: "That image is too large for layerlens to analyze",
  too_many_pulls: "This server is already busy with other pulls",
};

export function pullErrorHeading(code: string): string {
  return ERROR_HEADINGS[code] ?? "That image could not be analyzed";
}

export interface RateSample {
  at: number;
  bytes: number;
}

export interface RateEstimate {
  bytesPerSecond: number | null;
  etaSeconds: number | null;
}

const NO_RATE: RateEstimate = { bytesPerSecond: null, etaSeconds: null };

/** DESIGN §4.4: no ETA until throughput has had time to settle. */
export const ETA_MIN_SAMPLE_MS = 5_000;

/** Samples older than this fall out of the window, so the rate tracks reality. */
const RATE_WINDOW = 12;

/**
 * Throughput and a remaining-time estimate from successive byte samples.
 *
 * A window rather than a whole-pull average: a 25 GiB pull that starts on a
 * fast CDN edge and then slows down would otherwise keep quoting the opening
 * burst for minutes, and the number a user checks a progress bar for is what
 * is happening *now*.
 */
export function estimateRate(samples: readonly RateSample[], bytesTotal?: number): RateEstimate {
  const first = samples[0];
  const last = samples[samples.length - 1];
  if (first === undefined || last === undefined || samples.length < 2) {
    return NO_RATE;
  }
  const spanMs = last.at - first.at;
  const deltaBytes = last.bytes - first.bytes;
  if (spanMs <= 0 || deltaBytes < 0) {
    return NO_RATE;
  }
  const bytesPerSecond = (deltaBytes * 1000) / spanMs;
  if (spanMs < ETA_MIN_SAMPLE_MS || bytesPerSecond <= 0 || bytesTotal === undefined) {
    return { bytesPerSecond, etaSeconds: null };
  }
  return {
    bytesPerSecond,
    etaSeconds: Math.max(0, bytesTotal - last.bytes) / bytesPerSecond,
  };
}

/**
 * Accumulates byte samples for one pull. Keyed on the pull id so a retry of
 * the same reference starts from a clean window rather than inheriting the
 * previous attempt's rate.
 */
export function usePullRate(pull: PullStatus): RateEstimate {
  const store = useRef<{ id: string; samples: RateSample[] }>({ id: pull.id, samples: [] });
  const [estimate, setEstimate] = useState<RateEstimate>(NO_RATE);
  const { id, bytesDone, bytesTotal } = pull;

  useEffect(() => {
    if (store.current.id !== id) {
      store.current = { id, samples: [] };
    }
    const { samples } = store.current;
    samples.push({ at: Date.now(), bytes: bytesDone });
    if (samples.length > RATE_WINDOW) {
      samples.shift();
    }
    setEstimate(estimateRate(samples, bytesTotal));
  }, [id, bytesDone, bytesTotal]);

  return estimate;
}

function stepState(phase: PullPhase, index: number, pull: PullStatus): "done" | "active" | "todo" {
  const current = PULL_PHASES.findIndex((entry) => entry.id === phase);
  if (pull.state === "done") {
    return "done";
  }
  if (index < current) {
    return "done";
  }
  if (index > current) {
    return "todo";
  }
  return pull.state === "error" || pull.state === "cancelled" ? "todo" : "active";
}

const STEP_WORDS: Record<"done" | "active" | "todo", string> = {
  done: "complete",
  active: "in progress",
  todo: "not started",
};

function PhaseList({ pull }: { pull: PullStatus }) {
  const phase = pullPhase(pull);
  return (
    <ol className="ll-steps" data-testid="pull-steps">
      {PULL_PHASES.map((entry, index) => {
        const state = stepState(phase, index, pull);
        return (
          <li
            key={entry.id}
            className="ll-step"
            data-step={entry.id}
            data-state={state}
            {...(state === "active" ? { "aria-current": "step" as const } : {})}
          >
            <span className="ll-step-dot" aria-hidden="true" />
            <span>{entry.label}</span>
            {/* Status as words, never as the dot's color alone (DESIGN §7). */}
            <span className="sr-only"> — {STEP_WORDS[state]}</span>
          </li>
        );
      })}
    </ol>
  );
}

function TransferDetail({ pull, rate }: { pull: PullStatus; rate: RateEstimate }) {
  const percent = pullPercent(pull);
  const total = pull.bytesTotal;
  const sizes =
    total === undefined
      ? formatBytes(pull.bytesDone)
      : `${formatBytes(pull.bytesDone)} of ${pull.bytesEstimated ? "about " : ""}${formatBytes(total)}`;
  const layers =
    pull.layersTotal === undefined
      ? null
      : `${String(pull.layersDone)} of ${String(pull.layersTotal)} layers`;
  const eta = rate.etaSeconds === null ? "" : formatSoftEta(rate.etaSeconds);

  return (
    <>
      <div
        className="ll-pbar"
        role="progressbar"
        aria-label={`Downloading ${pull.reference}`}
        {...(percent === null
          ? { "aria-valuetext": "size not known yet" }
          : { "aria-valuenow": percent, "aria-valuemin": 0, "aria-valuemax": 100 })}
        data-testid="pull-bar"
      >
        <i className={percent === null ? "ll-pbar-unknown" : ""} style={{ width: `${String(percent ?? 100)}%` }} />
      </div>
      <p className="ll-pull-numbers" data-testid="pull-numbers">
        <span className="ll-num">{sizes}</span>
        {/* The daemon path has no manifest, so its total is the daemon's own
            estimate — saying so is the difference between a wrong number and
            an approximate one. */}
        {pull.bytesEstimated ? <span className="ll-chip">estimate</span> : null}
        {layers === null ? null : <span className="ll-num">{layers}</span>}
        {rate.bytesPerSecond === null ? null : (
          <span className="ll-num">{formatThroughput(rate.bytesPerSecond)}</span>
        )}
        {eta === "" ? null : <span className="text-text-muted">{eta}</span>}
      </p>
      {pull.layersSkipped > 0 ? (
        <p className="text-text-muted m-0 text-[12px]" data-testid="pull-skipped">
          {pull.layersSkipped} layer{pull.layersSkipped === 1 ? "" : "s"} already analyzed
        </p>
      ) : null}
    </>
  );
}

/**
 * One pull, in whatever state it is in (DESIGN §4.4 and §9 rows 12–15).
 *
 * The card stays mounted across tab switches because it lives above the tab
 * strip, which is the design's "state is never lost from view" rule made
 * structural rather than remembered.
 */
export function PullProgressCard({
  pull,
  rate = NO_RATE,
  onCancel,
  onRetry,
  onDismiss,
}: {
  pull: PullStatus;
  rate?: RateEstimate;
  onCancel?: (() => void) | undefined;
  onRetry?: (() => void) | undefined;
  onDismiss?: (() => void) | undefined;
}) {
  const active = pull.state === "resolving" || pull.state === "running";
  const milestone = pullMilestone(pull);

  return (
    <div
      className="ll-pull"
      data-state={pull.state}
      data-testid={`pull-${pull.reference}`}
      role="group"
      aria-label={`Pull of ${pull.reference}`}
    >
      <div className="ll-pull-head">
        <ImageRefText refName={pull.reference} className="min-w-0 flex-1 font-mono text-[13px]" />
        <span className="ll-chip">{pull.source === "docker" ? "docker" : "registry"}</span>
        {/* DESIGN §4.4: Cancel is always enabled while the pull is running. */}
        {active && onCancel ? (
          <button type="button" className="ll-btn-ghost" onClick={onCancel}>
            Cancel
          </button>
        ) : null}
        {!active && onDismiss ? (
          <button
            type="button"
            className="ll-icon-btn text-text-muted h-6 w-6 flex-none border-0 text-[14px]"
            aria-label={`Dismiss ${pull.reference}`}
            title="Dismiss"
            onClick={onDismiss}
          >
            <span aria-hidden="true">✕</span>
          </button>
        ) : null}
      </div>

      <PhaseList pull={pull} />

      {pull.state === "resolving" ? (
        <div className="ll-pbar ll-pbar-indeterminate" aria-hidden="true">
          <i />
        </div>
      ) : null}

      {pull.state === "running" ? <TransferDetail pull={pull} rate={rate} /> : null}

      {pull.state === "done" ? (
        <p className="text-text-muted m-0 text-[12.5px]" data-testid="pull-done">
          Analyzed — it is in the Analyzed tab now.
        </p>
      ) : null}

      {pull.state === "cancelled" ? (
        <p className="text-text-muted m-0 text-[12.5px]" data-testid="pull-cancelled">
          Pull cancelled.
        </p>
      ) : null}

      {pull.state === "error" && pull.error ? (
        <div role="alert" data-testid="pull-error">
          <p className="text-removed-strong m-0 text-[13px] font-[600]">
            {pullErrorHeading(pull.error.code)}
          </p>
          {/* The server's message verbatim: §6.1 guarantees it leaks nothing,
              and it is written to be the sentence the user reads. */}
          <p className="text-text-muted m-0 [overflow-wrap:anywhere]">{pull.error.message}</p>
          {onRetry ? (
            <button type="button" className="ll-btn-ghost mt-2" onClick={onRetry}>
              Retry
            </button>
          ) : null}
        </div>
      ) : null}

      <p aria-live="polite" className="sr-only">
        {milestone}
      </p>
    </div>
  );
}

/** The card plus its throughput sampling — the only stateful part. */
export function LivePullProgress(props: {
  pull: PullStatus;
  onCancel?: (() => void) | undefined;
  onRetry?: (() => void) | undefined;
  onDismiss?: (() => void) | undefined;
}) {
  const rate = usePullRate(props.pull);
  return <PullProgressCard {...props} rate={rate} />;
}
