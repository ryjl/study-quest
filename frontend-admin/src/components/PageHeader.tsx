import type { ReactNode } from 'react';
import { ChevronRight } from 'lucide-react';

// Unified page header replacing the ad-hoc <h1 className="text-2xl font-bold">
// scattered across every page. Linear-influenced: smaller title (the sidebar
// already tells you where you are), muted breadcrumbs with a chevron divider,
// and a sticky + backdrop-blur top bar so actions stay reachable while the
// page scrolls.

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
    <div className="sticky top-0 z-20 -mx-8 mb-6 border-b border-border/60 bg-bg/80 px-8 pb-4 pt-2 backdrop-blur">
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
                    {!last && <ChevronRight size={12} className="mx-1 text-muted" />}
                  </span>
                );
              })}
            </div>
          )}
          <h1 className="text-xl font-semibold tracking-tight text-txt">{title}</h1>
          {description && <p className="mt-1 text-sm text-muted">{description}</p>}
        </div>
        {actions && <div className="flex flex-shrink-0 items-center gap-2">{actions}</div>}
      </div>
    </div>
  );
}
