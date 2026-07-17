import type { ReactNode } from 'react';

// Unified page header replacing the ad-hoc <h1 className="text-2xl font-bold">
// scattered across every page. Keeps the border-b + pb-4 + mb-6 rhythm that
// Settings/Courses already used, but adds optional breadcrumb + actions slots
// so new pages don't reinvent the header each time.

export interface Breadcrumb {
  label: string;
  to?: string; // last crumb (current page) has no `to`
}

export function PageHeader({
  title,
  description,
  breadcrumb,
  actions,
}: {
  title: string;
  description?: string;
  breadcrumb?: Breadcrumb[];
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 border-b border-border pb-4">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          {breadcrumb && breadcrumb.length > 0 && (
            // Muted "组名 › ..." prefix; last crumb is the current page and
            // gets text-txt so the user knows where they are.
            <div className="mb-1 flex flex-wrap items-center text-xs text-muted">
              {breadcrumb.map((c, i) => {
                const last = i === breadcrumb.length - 1;
                return (
                  <span key={i} className="flex items-center">
                    <span className={last ? 'font-medium text-txt' : undefined}>{c.label}</span>
                    {!last && <span className="mx-1.5 text-muted">›</span>}
                  </span>
                );
              })}
            </div>
          )}
          <h1 className="text-2xl font-bold text-txt">{title}</h1>
          {description && <p className="mt-1 text-sm text-muted">{description}</p>}
        </div>
        {actions && <div className="flex flex-shrink-0 items-center gap-2">{actions}</div>}
      </div>
    </div>
  );
}
