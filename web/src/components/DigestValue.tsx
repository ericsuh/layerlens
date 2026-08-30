import { useCallback, useEffect, useState } from "react";

import { shortDigest, shortHex } from "../lib/format";
import { Tooltip } from "./ui/tooltip";

interface CopyRowProps {
  label: string;
  value: string;
}

function CopyRow({ label, value }: CopyRowProps) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) {
      return;
    }
    const timer = window.setTimeout(() => {
      setCopied(false);
    }, 1500);
    return () => {
      window.clearTimeout(timer);
    };
  }, [copied]);

  const copy = useCallback(() => {
    // navigator.clipboard is absent on insecure origins; failing quietly and
    // leaving the full value visible in the tooltip is the graceful fallback.
    void navigator.clipboard
      ?.writeText(value)
      .then(() => {
        setCopied(true);
      })
      .catch(() => {
        setCopied(false);
      });
  }, [value]);

  return (
    <div className="flex flex-col gap-1">
      <span className="text-label text-text-muted uppercase">{label}</span>
      <div className="flex items-center gap-2">
        <span className="font-mono text-[12px] break-all">{value}</span>
        <button type="button" className="ll-btn-ghost flex-none" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
    </div>
  );
}

/**
 * A digest rendered middle-truncated with the full value (and a copy button)
 * behind a tooltip — DESIGN §3's "full value recoverable" rule. Digests are
 * data, not controls, so the trigger deliberately gets no hover chrome and no
 * pointer cursor (DESIGN §1.2); it is focusable only so the tooltip is
 * keyboard-reachable.
 */
export function DigestValue({
  digest,
  label = "Digest",
  secondary,
  withPrefix = true,
  className = "",
}: {
  digest: string;
  label?: string;
  secondary?: { label: string; value: string };
  withPrefix?: boolean;
  className?: string;
}) {
  return (
    <Tooltip
      content={
        <div className="flex flex-col gap-3">
          <CopyRow label={label} value={digest} />
          {secondary ? <CopyRow label={secondary.label} value={secondary.value} /> : null}
        </div>
      }
    >
      <span
        tabIndex={0}
        role="note"
        aria-label={`${label} ${digest}`}
        className={`ll-mono text-text-muted rounded whitespace-nowrap ${className}`.trim()}
      >
        {withPrefix ? shortDigest(digest) : shortHex(digest)}
      </span>
    </Tooltip>
  );
}
