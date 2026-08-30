import { Link } from "wouter";

/**
 * The shared empty/error block (DESIGN §4.5): no illustration, a 15px title, a
 * muted explanation, at most one action. Error text comes from the server's
 * §6.1 `message`, which is written to be shown verbatim.
 */
export function EmptyPanel({
  title,
  detail,
  action,
}: {
  title: string;
  detail: string;
  action?: { label: string; href: string };
}) {
  return (
    <div className="px-8 py-16 text-center">
      <p className="text-section m-0 mb-1.5">{title}</p>
      <p className="text-text-muted mx-auto m-0 max-w-[420px]">{detail}</p>
      {action ? (
        <Link href={action.href} className="ll-btn-ghost mt-4 inline-block">
          {action.label}
        </Link>
      ) : null}
    </div>
  );
}

export function ErrorPanel({
  title,
  detail,
  action,
}: {
  title: string;
  detail: string;
  action?: { label: string; href: string };
}) {
  return (
    <div
      role="alert"
      className="border-border bg-surface shadow-panel rounded-[10px] border px-8 py-16 text-center"
    >
      <p className="text-section text-removed-strong m-0 mb-1.5">{title}</p>
      <p className="text-text-muted mx-auto m-0 max-w-[420px] [overflow-wrap:anywhere]">{detail}</p>
      {action ? (
        <Link href={action.href} className="ll-btn-ghost mt-4 inline-block">
          {action.label}
        </Link>
      ) : null}
    </div>
  );
}
