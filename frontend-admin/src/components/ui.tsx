import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { subjectMeta, type SubjectMeta } from '../lib/types';
import { useSubjects } from '../lib/useSubjects';
import { relativeTime } from '../lib/format';

// Reusable UI primitives shared across all admin pages.

// ---- Modal ----
export function Modal({
  open,
  onClose,
  title,
  children,
  size = 'md',
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  size?: 'sm' | 'md' | 'lg' | 'xl';
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    document.body.style.overflow = 'hidden';
    return () => {
      window.removeEventListener('keydown', onKey);
      document.body.style.overflow = '';
    };
  }, [open, onClose]);

  if (!open) return null;

  const widths = { sm: 'max-w-sm', md: 'max-w-lg', lg: 'max-w-2xl', xl: 'max-w-4xl' };

  return (
    <div className="fixed inset-0 z-[1500] flex items-start justify-center overflow-auto bg-black/60 backdrop-blur-sm p-4" onClick={onClose}>
      <div
        className={`mt-[8vh] w-full ${widths[size]} rounded-2xl border border-border bg-card p-6 shadow-2xl`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between border-b border-border pb-3">
          <h2 className="text-lg font-bold text-txt">{title}</h2>
          <button onClick={onClose} className="text-2xl leading-none text-muted hover:text-txt" aria-label="关闭">
            ×
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

// ---- Drawer (for subtitle mgmt, user details, etc.) ----
export function Drawer({
  open,
  onClose,
  title,
  children,
  width = 'md',
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  width?: 'md' | 'lg';
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  if (!open) return null;
  const w = width === 'lg' ? 'max-w-2xl' : 'max-w-md';

  return (
    <div className="fixed inset-0 z-[1500] flex justify-end bg-black/50 backdrop-blur-sm" onClick={onClose}>
      <div
        className={`h-full w-full ${w} overflow-auto border-l border-border bg-card p-6 shadow-2xl`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center justify-between border-b border-border pb-3">
          <h2 className="text-lg font-bold text-txt">{title}</h2>
          <button onClick={onClose} className="text-2xl leading-none text-muted hover:text-txt" aria-label="关闭">
            ×
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

// ---- Badge helpers ----
export function SubjectBadge({ subject }: { subject: string }) {
  // Subscribe to the subjects query so this component re-renders when the
  // catalog lands. The old version read the non-reactive module cache only,
  // which meant a first paint before the query resolved showed the raw key
  // ("english") + grey fallback and never corrected (the cache filling doesn't
  // trigger a re-render). Resolving from the reactive list first closes that
  // race on every page that uses this badge, regardless of whether the page
  // itself calls useSubjects().
  const subjectsQ = useSubjects();
  const found = subjectsQ.data?.find((x) => x.key === subject) as SubjectMeta | undefined;
  const s = found ?? subjectMeta(subject);
  return (
    <span className="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-xs font-semibold" style={{ backgroundColor: `${s.color}20`, color: s.color }}>
      <span>{s.emoji}</span>
      {s.label}
    </span>
  );
}

export function Tag({ children, color = '#10b981' }: { children: ReactNode; color?: string }) {
  return (
    <span className="inline-block rounded-md px-2 py-0.5 text-xs font-medium" style={{ backgroundColor: `${color}20`, color }}>
      {children}
    </span>
  );
}

// ---- EmptyState ----
export function EmptyState({ icon, title, hint, action }: { icon?: string; title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-border bg-card p-10 text-center">
      {icon && <div className="mb-3 text-4xl opacity-50">{icon}</div>}
      <h3 className="mb-1 text-base font-semibold text-txt">{title}</h3>
      {hint && <p className="mb-4 text-sm text-muted">{hint}</p>}
      {action}
    </div>
  );
}

// ---- StatCard ----
export function StatCard({ label, value, hint, icon, color = '#8b5cf6' }: { label: string; value: ReactNode; hint?: string; icon?: ReactNode; color?: string }) {
  return (
    <div className="card flex items-center justify-between">
      <div>
        <div className="mb-1 text-sm font-medium text-muted">{label}</div>
        <div className="text-3xl font-bold text-txt">{value}</div>
        {hint && <div className="mt-1 text-xs text-muted">{hint}</div>}
      </div>
      {icon && (
        <div className="rounded-xl p-3" style={{ backgroundColor: `${color}1a`, color }}>
          {icon}
        </div>
      )}
    </div>
  );
}

// ---- Spinner ----
export function Spinner({ size = 20 }: { size?: number }) {
  return (
    <svg className="animate-spin text-primary" width={size} height={size} viewBox="0 0 24 24" fill="none">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-90" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  );
}

// ---- LoadingState ----
export function LoadingState({ label = '加载中...' }: { label?: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-8 text-muted">
      <Spinner /> <span className="text-sm">{label}</span>
    </div>
  );
}

// ---- Section (collapsible content region) ----
// Splits long pages/drawers into collapsible cards. The header toggles open
// state but the `right` action slot stops propagation so buttons inside it
// don't accidentally collapse the section.
export function Section({
  title,
  icon,
  description,
  defaultOpen = true,
  collapsible = true,
  right,
  badge,
  children,
}: {
  title: string;
  icon?: string;
  description?: string;
  defaultOpen?: boolean;
  collapsible?: boolean;
  right?: ReactNode;
  badge?: ReactNode;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  // When non-collapsible the section is always open; chevron is hidden.
  const isOpen = collapsible ? open : true;

  return (
    <div className="overflow-hidden rounded-2xl border border-border bg-card">
      <div
        className={`flex items-center gap-3 px-5 py-3.5 ${collapsible ? 'cursor-pointer hover:bg-card-2' : ''}`}
        onClick={() => collapsible && setOpen((o) => !o)}
      >
        {collapsible && (
          <span className={`text-xs text-muted transition-transform duration-150 ${isOpen ? 'rotate-90' : ''}`}>▶</span>
        )}
        {icon && <span className="text-base">{icon}</span>}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="font-semibold text-txt">{title}</h3>
            {badge && <span className="rounded bg-card-2 px-1.5 py-0.5 text-xs text-muted">{badge}</span>}
          </div>
          {description && <p className="mt-0.5 text-xs text-muted">{description}</p>}
        </div>
        {/* right actions: stopPropagation so clicks here don't toggle the header */}
        {right && (
          <div className="flex items-center gap-2" onClick={(e) => e.stopPropagation()}>
            {right}
          </div>
        )}
      </div>
      {isOpen && <div className="border-t border-border px-5 py-4">{children}</div>}
    </div>
  );
}

// ---- StatusCard (themed dashboard banner) ----
const toneStyles: Record<'ok' | 'warn' | 'danger' | 'info', string> = {
  ok: 'border-good/30 bg-good/10',
  warn: 'border-warn/30 bg-warn/10',
  danger: 'border-bad/30 bg-bad/10',
  info: 'border-primary/30 bg-primary/10',
};

export function StatusCard({
  tone,
  icon,
  title,
  children,
  action,
}: {
  tone: 'ok' | 'warn' | 'danger' | 'info';
  icon: string;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className={`flex items-center gap-4 rounded-2xl border p-4 ${toneStyles[tone]}`}>
      <div className="text-2xl">{icon}</div>
      <div className="min-w-0 flex-1">
        <div className="font-semibold text-txt">{title}</div>
        {children && <div className="mt-0.5 text-sm text-muted">{children}</div>}
      </div>
      {action && <div className="flex-shrink-0">{action}</div>}
    </div>
  );
}

// ---- TodoItem (actionable dashboard row) ----
// Caller hides rows with count 0; the count pill stays neutral so it reads
// cleanly regardless of magnitude.
export function TodoItem({
  icon,
  label,
  count,
  hint,
  to,
}: {
  icon: string;
  label: string;
  count: number;
  hint?: string;
  to: string;
}) {
  const navigate = useNavigate();
  return (
    <div
      className="flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2.5 transition-colors hover:bg-card-2"
      onClick={() => navigate(to)}
    >
      <div className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg bg-primary/10 text-base">
        {icon}
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-sm text-txt">{label}</div>
        {hint && <div className="text-xs text-muted">{hint}</div>}
      </div>
      <span className="flex-shrink-0 rounded-full bg-card-2 px-2 py-0.5 text-xs font-semibold text-txt">{count}</span>
      <span className="text-sm text-muted">→</span>
    </div>
  );
}

// ---- ActivityFeed (vertical timeline) ----
export interface ActivityItem {
  id: string | number;
  icon: string;
  title: string;
  detail?: string;
  time: string; // ISO timestamp; rendered via relativeTime
}

export function ActivityFeed({ items, emptyHint = '暂无动态' }: { items: ActivityItem[]; emptyHint?: string }) {
  if (items.length === 0) {
    return <p className="py-6 text-center text-sm text-muted">{emptyHint}</p>;
  }
  return (
    <div>
      {items.map((it, i) => {
        const last = i === items.length - 1;
        return (
          <div key={it.id} className="flex gap-3">
            {/* Left column: icon circle + connecting line (hidden on last item) */}
            <div className="flex flex-col items-center">
              <div className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-card-2 text-sm">
                {it.icon}
              </div>
              {!last && <div className="my-1 w-px flex-1 border-l border-border" />}
            </div>
            <div className={`flex min-w-0 flex-1 ${last ? 'pb-1' : 'pb-4'}`}>
              <div className="min-w-0 flex-1">
                <div className="text-sm text-txt">{it.title}</div>
                {it.detail && <div className="mt-0.5 text-xs text-muted">{it.detail}</div>}
              </div>
              <div className="ml-2 flex-shrink-0 text-xs text-muted">{relativeTime(it.time)}</div>
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ---- Tabs (simple horizontal tab switcher) ----
export interface TabItem {
  key: string;
  label: string;
  icon?: string;
  badge?: ReactNode;
}

export function Tabs({ tabs, value, onChange }: { tabs: TabItem[]; value: string; onChange: (key: string) => void }) {
  return (
    <div className="border-b border-border">
      <div className="flex gap-1">
        {tabs.map((t) => {
          const active = t.key === value;
          return (
            <button
              key={t.key}
              onClick={() => onChange(t.key)}
              className={`flex items-center gap-1.5 border-b-2 px-4 py-2.5 text-sm transition-colors ${
                active
                  ? 'border-primary font-semibold text-primary'
                  : 'border-transparent text-muted hover:text-txt'
              }`}
            >
              {t.icon && <span>{t.icon}</span>}
              {t.label}
              {t.badge}
            </button>
          );
        })}
      </div>
    </div>
  );
}

// ---- DropdownMenu (lightweight "⋯" menu, no external lib) ----
// Click-outside via document listener + Esc to close. `items` may be a custom
// ReactNode for non-action content (e.g. a filter form embedded in a menu).
export interface DropdownMenuItem {
  label: string;
  icon?: string;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
}

export function DropdownMenu({
  trigger,
  items,
  align = 'right',
}: {
  trigger?: ReactNode;
  items: DropdownMenuItem[] | ReactNode;
  align?: 'left' | 'right';
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onPointer = (e: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('pointerdown', onPointer);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointer);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const isItemList = Array.isArray(items);

  return (
    <div className="relative" ref={rootRef}>
      {trigger ? (
        <div onClick={() => setOpen((o) => !o)}>{trigger}</div>
      ) : (
        <button className="btn-ghost btn-sm" onClick={() => setOpen((o) => !o)} aria-label="更多操作">
          ⋯
        </button>
      )}
      {open && (
        <div
          className={`absolute z-50 mt-1 min-w-[160px] rounded-xl border border-border bg-card py-1 shadow-lg ${
            align === 'right' ? 'right-0' : 'left-0'
          }`}
        >
          {isItemList
            ? (items as DropdownMenuItem[]).map((it, i) => (
                <button
                  key={i}
                  disabled={it.disabled}
                  onClick={() => {
                    if (it.disabled) return;
                    it.onClick();
                    setOpen(false);
                  }}
                  className={`flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors hover:bg-card-2 disabled:cursor-not-allowed disabled:opacity-50 ${
                    it.danger ? 'text-bad' : 'text-txt'
                  }`}
                >
                  {it.icon && <span>{it.icon}</span>}
                  {it.label}
                </button>
              ))
            : items}
        </div>
      )}
    </div>
  );
}
