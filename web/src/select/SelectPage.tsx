import { useCallback, useMemo, useReducer, useState } from "react";
import { useLocation } from "wouter";

import { ApiError } from "../api/client";
import {
  isPullActive,
  useCancelPull,
  useDockerImagesQuery,
  useImagesQuery,
  useMetaQuery,
  usePullsQuery,
  useStartPull,
} from "../api/queries";
import type { PullStatus } from "../api/types";
import { ErrorPanel } from "../components/states";
import { SIDE_LABEL } from "../components/identity";
import type { Side } from "../components/identity";
import { compareHref, DEFAULT_FILTER, DEFAULT_PATH } from "../lib/urlstate";
import { DEFAULT_ALLOWED_PATTERNS } from "../lib/refcheck";
import { AnalyzedList, AnalyzedListSkeleton } from "./AnalyzedList";
import { DockerList } from "./DockerList";
import { LivePullProgress } from "./PullProgress";
import { RegistryForm } from "./RegistryForm";
import { SlotCard } from "./SlotCard";
import { SourceTabs } from "./SourceTabs";
import type { SourceTabId } from "./SourceTabs";
import { bothFilled, initialSlotState, sameImage, slotReducer } from "./slots";

/**
 * The image-selection view (DESIGN §4). Slot state is local and ephemeral —
 * only a completed pair becomes URL state, on the way to /compare.
 */
export function SelectPage() {
  const [slots, dispatch] = useReducer(slotReducer, initialSlotState);
  const [tab, setTab] = useState<SourceTabId>("analyzed");
  const [, navigate] = useLocation();
  const query = useImagesQuery();
  const meta = useMetaQuery();
  const docker = useDockerImagesQuery();
  const pulls = usePullsQuery();

  // Two mutation handles for one endpoint: the registry form shows a rejected
  // POST inline under its input (states #7–#13), and a Docker-tab failure must
  // not appear there — or vice versa.
  const registryPull = useStartPull();
  const sourcePull = useStartPull();
  const cancel = useCancelPull();

  /** Terminal pulls the user has waved away; client-side only (§4.4). */
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(new Set());
  const dismiss = useCallback((id: string) => {
    setDismissed((previous) => new Set(previous).add(id));
  }, []);

  const images = useMemo(() => query.data?.images ?? [], [query.data]);
  const byId = useMemo(() => new Map(images.map((image) => [image.id, image])), [images]);

  // The served allowlist, not our copy of it: an operator can narrow it, and a
  // form that promised registries the server refuses would be lying. The
  // bundled default only covers the window before /meta answers.
  const allowedRegistries = useMemo(() => {
    const served = meta.data?.allowedRegistries;
    return served !== undefined && served.length > 0 ? served : DEFAULT_ALLOWED_PATTERNS;
  }, [meta.data]);

  const activePulls = useMemo(() => pulls.data?.pulls ?? [], [pulls.data]);
  /**
   * What the list above the tabs shows. A pull leaves it when its image has
   * actually appeared in the Analyzed list — the card's whole claim is "this
   * is becoming an image you can select", so it is only redundant once that is
   * true. Errors and cancellations stay until dismissed.
   */
  const visiblePulls = useMemo(
    () =>
      activePulls.filter((pull: PullStatus) => {
        if (dismissed.has(pull.id)) {
          return false;
        }
        if (pull.state === "done" && pull.imageId !== undefined) {
          return !byId.has(pull.imageId);
        }
        return true;
      }),
    [activePulls, dismissed, byId],
  );

  /** References already being pulled, so a second click cannot double-submit. */
  const analyzing = useMemo(
    () => new Set(activePulls.filter(isPullActive).map((pull) => pull.reference)),
    [activePulls],
  );

  const ready = bothFilled(slots);
  const duplicate = sameImage(slots);

  const compare = () => {
    if (!ready) {
      return;
    }
    navigate(
      compareHref({
        left: slots.a,
        right: slots.b,
        l: null,
        r: null,
        path: DEFAULT_PATH,
        filter: DEFAULT_FILTER,
      }),
    );
  };

  const retry = (pull: PullStatus) => {
    // The failed attempt is dismissed as the retry starts: the server keeps
    // per-layer checkpoints, so this resumes rather than restarts (§6.3), and
    // leaving the old card up would read as two pulls of the same image.
    dismiss(pull.id);
    sourcePull.mutate({ source: pull.source, reference: pull.reference });
  };

  const registryError =
    registryPull.error instanceof ApiError
      ? { code: registryPull.error.code, message: registryPull.error.message }
      : null;

  return (
    <div className="mx-auto max-w-[960px] p-8">
      <h1 className="text-page-title m-0 mb-1">Compare two images</h1>
      <p className="text-text-muted m-0 mb-6">
        Pick an image for each slot, then compare their layers and filesystems.
      </p>

      <div className="grid grid-cols-2 gap-4 max-[719px]:grid-cols-1">
        {(["a", "b"] as const).map((side: Side) => (
          <SlotCard
            key={side}
            side={side}
            image={slots[side] === null ? null : (byId.get(slots[side]) ?? null)}
            armed={slots.armed === side}
            onArm={() => {
              dispatch({ type: "arm", side });
            }}
            onClear={() => {
              dispatch({ type: "clear", side });
            }}
          />
        ))}
      </div>

      {/* DESIGN §7: arming is announced, because it changes where the next
          plain click lands and that is not otherwise spoken. */}
      <p aria-live="polite" className="sr-only">
        {`Image ${SIDE_LABEL[slots.armed]} slot active`}
      </p>

      <div className="my-6 mb-8 flex items-center justify-center gap-3">
        <button
          type="button"
          className="ll-btn-primary"
          disabled={!ready}
          onClick={compare}
          data-testid="compare-button"
        >
          Compare layers →
        </button>
        <span className="text-text-muted text-[12px]" data-testid="compare-hint">
          {ready
            ? duplicate
              ? "Both slots contain the same image — every layer will be shared."
              : ""
            : "Choose two images to compare"}
        </span>
      </div>

      {/* A pull the server refused before it became a pull at all: a Docker
          analyze with no daemon, or a retry the cache no longer has room for.
          It belongs next to the pull cards rather than inside a source panel,
          because the tab the user is looking at by then may not be the one
          they started it from. */}
      {sourcePull.error instanceof ApiError ? (
        <div className="ll-pull mb-6" role="alert" data-testid="start-error">
          <div className="ll-pull-head">
            <span className="text-removed-strong flex-1 [overflow-wrap:anywhere]">
              {sourcePull.error.message}
            </span>
            <button
              type="button"
              className="ll-icon-btn text-text-muted h-6 w-6 flex-none border-0 text-[14px]"
              aria-label="Dismiss this message"
              title="Dismiss"
              onClick={() => {
                sourcePull.reset();
              }}
            >
              <span aria-hidden="true">✕</span>
            </button>
          </div>
        </div>
      ) : null}

      {/* Above the tab strip on purpose (DESIGN §4.4): a pull the user started
          from the Registry tab must stay in view while they browse the
          Analyzed tab for the other half of the comparison. */}
      {visiblePulls.length > 0 ? (
        <div className="mb-6 flex flex-col gap-3" data-testid="pull-list">
          {visiblePulls.map((pull) => (
            <LivePullProgress
              key={pull.id}
              pull={pull}
              onCancel={() => {
                cancel.mutate(pull.id);
              }}
              onRetry={() => {
                retry(pull);
              }}
              onDismiss={() => {
                dismiss(pull.id);
              }}
            />
          ))}
        </div>
      ) : null}

      <SourceTabs
        active={tab}
        onChange={setTab}
        analyzedCount={query.isSuccess ? images.length : null}
        dockerAvailable={docker.data === undefined ? null : docker.data.available}
      >
        {tab === "analyzed" ? (
          <>
            {query.isPending ? <AnalyzedListSkeleton /> : null}
            {query.isError ? (
              <ErrorPanel
                title="Analyzed images could not be loaded"
                detail={
                  query.error instanceof ApiError
                    ? query.error.message
                    : "The image list request failed."
                }
              />
            ) : null}
            {query.isSuccess ? (
              <AnalyzedList
                images={images}
                slots={slots}
                onPick={(id) => {
                  dispatch({ type: "pick", id });
                }}
                onSet={(side, id) => {
                  dispatch({ type: "set", side, id });
                }}
              />
            ) : null}
          </>
        ) : null}

        {tab === "docker" ? (
          <>
            {docker.isPending ? (
              <AnalyzedListSkeleton label="Loading Docker images" testId="docker-skeleton" />
            ) : null}
            {/* State #6: the daemon answered and then failed. That is the only
                Docker case with an action, and the action is a refetch. */}
            {docker.isError ? (
              <ErrorPanel
                title="The Docker daemon could not be queried"
                detail={
                  docker.error instanceof ApiError
                    ? docker.error.message
                    : "The Docker image listing request failed."
                }
                retry={{
                  label: "Retry",
                  onClick: () => {
                    void docker.refetch();
                  },
                }}
              />
            ) : null}
            {docker.isSuccess ? (
              <DockerList
                listing={docker.data}
                slots={slots}
                analyzing={analyzing}
                onPick={(id) => {
                  dispatch({ type: "pick", id });
                }}
                onSet={(side, id) => {
                  dispatch({ type: "set", side, id });
                }}
                onAnalyze={(reference) => {
                  sourcePull.mutate({ source: "docker", reference });
                }}
              />
            ) : null}
          </>
        ) : null}

        {tab === "registry" ? (
          <RegistryForm
            allowedRegistries={allowedRegistries}
            submitting={registryPull.isPending}
            error={registryError}
            onEdit={() => {
              // A rejection is about the reference that was submitted; the
              // moment the user changes it, it is answering a stale question.
              if (registryPull.isError) {
                registryPull.reset();
              }
            }}
            onSubmit={(reference) => {
              registryPull.mutate({ source: "registry", reference });
            }}
          />
        ) : null}
      </SourceTabs>
    </div>
  );
}
