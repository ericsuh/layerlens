import { useId, useMemo, useState } from "react";

import { checkReference, friendlyRegistryNames } from "../lib/refcheck";
import type { RefVerdict } from "../lib/refcheck";

/**
 * The registry source panel (DESIGN §4.3 tab 3, §4.5, §9 rows 7–13).
 *
 * Validation is pre-flight and inline: the verdict under the input comes from
 * the local mirror of the server's rule (`lib/refcheck`), per keystroke, so a
 * typo or a registry this server will not pull from is answered before a
 * request exists. The server re-checks everything — see refcheck's header —
 * and its own rejection lands in the same inline area, so the user never has
 * to look in two places for "why not".
 */
export function RegistryForm({
  allowedRegistries,
  submitting,
  error,
  onSubmit,
  onEdit,
}: {
  allowedRegistries: readonly string[];
  submitting: boolean;
  /** The server's rejection of the last submit, shown verbatim (§6.1). */
  error: { code: string; message: string } | null;
  onSubmit: (reference: string) => void;
  /** Lets the caller clear a stale server error as soon as the user retypes. */
  onEdit: () => void;
}) {
  const [value, setValue] = useState("");
  const inputId = useId();
  const helpId = useId();
  const verdictId = useId();

  const verdict: RefVerdict = useMemo(
    () => checkReference(value, allowedRegistries),
    [value, allowedRegistries],
  );
  const names = useMemo(() => friendlyRegistryNames(allowedRegistries), [allowedRegistries]);
  const bad = verdict.kind === "invalid" || verdict.kind === "not-allowed";

  return (
    <form
      className="flex flex-col gap-2 px-8 py-8"
      onSubmit={(event) => {
        event.preventDefault();
        if (verdict.kind !== "ok" || submitting) {
          return;
        }
        onSubmit(value.trim());
      }}
    >
      <label htmlFor={inputId} className="text-label text-text-muted uppercase">
        Image reference
      </label>
      <div className="flex items-center gap-2">
        <input
          id={inputId}
          type="text"
          value={value}
          spellCheck={false}
          autoComplete="off"
          autoCapitalize="off"
          placeholder="ghcr.io/org/image:tag"
          aria-describedby={`${verdictId} ${helpId}`}
          aria-invalid={bad}
          // The red border is a utility rather than a component class because
          // utilities win the cascade over `@layer components` — a component
          // class here would simply never show (DESIGN §9 #7).
          className={`bg-surface text-text min-w-0 flex-1 rounded-md border px-3 py-2 font-mono text-[13px] ${
            bad ? "border-removed" : "border-border-strong"
          }`}
          data-testid="registry-input"
          onChange={(event) => {
            setValue(event.target.value);
            onEdit();
          }}
        />
        <button
          type="submit"
          className="ll-btn-primary flex-none"
          disabled={verdict.kind !== "ok" || submitting}
          data-testid="registry-submit"
        >
          {submitting ? "Starting…" : "Fetch & analyze"}
        </button>
      </div>

      {/* One live region for the whole verdict: the resolved registry, the
          parse error and the server's rejection are the same answer to the
          same question, and announcing them from three places would read as
          three unrelated events. */}
      <div id={verdictId} aria-live="polite" className="min-h-[20px]">
        {verdict.kind === "ok" ? (
          <p className="text-text-muted m-0 text-[12.5px]" data-testid="registry-verdict">
            → <span className="font-mono">{verdict.registry}</span>{" "}
            <span className="text-added-strong">✓ allowed</span>
          </p>
        ) : null}
        {verdict.kind === "invalid" ? (
          <p className="text-removed-strong m-0 text-[12.5px]" data-testid="registry-verdict">
            Not a valid image reference
          </p>
        ) : null}
        {verdict.kind === "not-allowed" ? (
          <p className="text-removed-strong m-0 text-[12.5px]" data-testid="registry-verdict">
            <span className="font-mono">{verdict.registry}</span> isn’t on the allowlist. Allowed:{" "}
            {names.join(", ")}.
          </p>
        ) : null}
        {error === null ? null : (
          <p
            role="alert"
            className="text-removed-strong m-0 text-[12.5px] [overflow-wrap:anywhere]"
            data-testid="registry-error"
          >
            {error.message}
          </p>
        )}
      </div>

      <p id={helpId} className="text-text-muted m-0 text-[12px]">
        Anonymous public pulls only, on linux/amd64. Allowed registries:{" "}
        {allowedRegistries.map((pattern, index) => (
          <span key={pattern}>
            {index === 0 ? "" : ", "}
            <span className="font-mono">{pattern}</span>
          </span>
        ))}
        .
      </p>
    </form>
  );
}
