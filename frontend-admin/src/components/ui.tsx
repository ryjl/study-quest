import { useEffect, useRef, useState, type ReactNode, type MouseEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { subjectMeta, type SubjectMeta } from '../lib/types';
import { useSubjects } from '../lib/useSubjects';
import { relativeTime } from '../lib/format';
import { resolveSubjectIcon } from '../lib/subjectIcon';
import { ChevronRight, MoreHorizontal, X } from 'lucide-react';

// Reusable UI primitives shared across all admin pages.
// Visual language: Linear/Notion — small radii, border-defined surfaces,
// restrained color, lucide-react icons. Most `icon` props accept ReactNode
// (so callers pass <LucideIcon size={14}/>) but still tolerate legacy emoji
// strings during the migration.

// clickOutsideOnly returns handlers that fire `onClose` ONLY when the pointer
// press STARTED on the overlay (not inside the panel). This is the fix for the
// "I selected text, dragged outside the modal, and it closed" problem: a drag
// that begins inside the panel is a text-selection gesture, not a dismiss
// intent, even if it ends on the overlay. Linear/Notion behave the same way.
//
// Implementation follows the Radix/Headless UI pattern: record where mousedown
// landed, then decide on click. We use click (not mouseup) as the trigger so
// touch devices (which synthesize click from tap) work without a separate
// touch handler.
//
// Usage: spread the two handlers onto the overlay div.
//   <div onMouseDown={h.onMouseDown} onClick={h.onClick}>
function clickOutsideOnly(onClose: () => void) {
  // True if the most recent mousedown landed on the overlay itself. Reset on
  // every mousedown so a stale value from a prior interaction can't leak in.
  const startedOnOverlay = useRef(false);
  return {
    onMouseDown: (e: MouseEvent) => {
      startedOnOverlay.current = e.target === e.currentTarget;
    },
    onClick: (e: MouseEvent) => {
      // Only dismiss when the click is on the overlay AND the press that
      // started this click also began on the overlay. A click whose mousedown
      // was inside the panel (text drag that ended outside) has
      // startedOnOverlay=false and is ignored.
      if (e.target === e.currentTarget && startedOnOverlay.current) {
        onClose();
      }
    },
  };
}

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

  // clickOutsideOnly 是个 hook(内部用 useRef),必须在所有 early return 之前无条件
  // 调用——否则 open 切换时 Modal 的 hook 数变化会触发 React #310
  // ("Rendered fewer hooks than expected")。下面 Drawer 同理。
  const widths = { sm: 'max-w-sm', md: 'max-w-lg', lg: 'max-w-2xl', xl: 'max-w-4xl' };
  const overlay = clickOutsideOnly(onClose);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-[1500] flex items-start justify-center overflow-auto bg-black/50 backdrop-blur-sm p-4"
      onMouseDown={overlay.onMouseDown}
      onClick={overlay.onClick}
    >
      <div className={`mt-[8vh] w-full ${widths[size]} rounded-xl border border-border bg-card shadow-lg`}>
        {/* Header: padding-separated, no border-b — cleaner than the old divider. */}
        <div className="flex items-center justify-between px-5 pt-5 pb-3">
          <h2 className="text-base font-semibold text-txt">{title}</h2>
          <button onClick={onClose} className="rounded-md p-1 text-muted transition-colors hover:bg-card-2 hover:text-txt" aria-label="关闭">
            <X size={18} />
          </button>
        </div>
        <div className="px-5 pb-5">{children}</div>
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

  // clickOutsideOnly 是 hook,必须在 early return 前调用(见 Modal 的同类注释)。
  const w = width === 'lg' ? 'max-w-2xl' : 'max-w-md';
  const overlay = clickOutsideOnly(onClose);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-[1500] flex justify-end bg-black/50 backdrop-blur-sm"
      onMouseDown={overlay.onMouseDown}
      onClick={overlay.onClick}
    >
      <div className={`h-full w-full ${w} overflow-auto border-l border-border bg-card shadow-lg`}>
        <div className="flex items-center justify-between px-5 pt-5 pb-3">
          <h2 className="text-base font-semibold text-txt">{title}</h2>
          <button onClick={onClose} className="rounded-md p-1 text-muted transition-colors hover:bg-card-2 hover:text-txt" aria-label="关闭">
            <X size={18} />
          </button>
        </div>
        <div className="px-5 pb-5">{children}</div>
      </div>
    </div>
  );
}

// ---- Badge helpers ----

// SubjectIcon renders the Lucide line icon for a subject key, tinted with the
// subject's color. Replaces the old emoji glyph — emoji render inconsistently
// across platforms and clash with the panel's icon language. Used by
// SubjectBadge and anywhere a bare subject icon is needed (covers, list
// headers, etc.). `size` defaults to 13 to match SubjectBadge's text size.
export function SubjectIcon({ subject, size = 13 }: { subject: string; size?: number }) {
  const subjectsQ = useSubjects();
  const found = subjectsQ.data?.find((x) => x.key === subject) as SubjectMeta | undefined;
  const s = found ?? subjectMeta(subject);
  const Icon = resolveSubjectIcon(subject);
  return <Icon size={size} style={{ color: s.color }} />;
}

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
    <span className="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium" style={{ backgroundColor: `${s.color}1a`, color: s.color }}>
      <SubjectIcon subject={subject} />
      {s.label}
    </span>
  );
}

export function Tag({ children, color = '#10b981' }: { children: ReactNode; color?: string }) {
  return (
    <span className="inline-block rounded-md px-1.5 py-0.5 text-xs font-medium" style={{ backgroundColor: `${color}1a`, color }}>
      {children}
    </span>
  );
}

// ---- EmptyState ----
export function EmptyState({ icon, title, hint, action }: { icon?: ReactNode; title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-border bg-card p-8 text-center">
      {icon && <div className="mb-3 text-muted">{icon}</div>}
      <h3 className="mb-1 text-sm font-semibold text-txt">{title}</h3>
      {hint && <p className="mb-4 max-w-sm text-sm text-muted">{hint}</p>}
      {action}
    </div>
  );
}

// ---- StatCard ----
// Minimal Linear-style stat: large tabular number, small muted label, an
// optional inline icon. No colored corner block — that read as dashboard
// template. Color, when passed, tints the icon only.
export function StatCard({ label, value, hint, icon, color = '#64748b' }: { label: string; value: ReactNode; hint?: string; icon?: ReactNode; color?: string }) {
  return (
    <div className="card">
      <div className="flex items-center justify-between">
        <div className="text-xs font-medium text-muted">{label}</div>
        {icon && <div style={{ color }}>{icon}</div>}
      </div>
      <div className="mt-2 text-2xl font-semibold tabular-nums text-txt">{value}</div>
      {hint && <div className="mt-1 text-xs text-muted">{hint}</div>}
    </div>
  );
}

// ---- Spinner ----
export function Spinner({ size = 16 }: { size?: number }) {
  return (
    <svg className="animate-spin text-muted" width={size} height={size} viewBox="0 0 24 24" fill="none">
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
  icon?: ReactNode;
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
    <div className="overflow-hidden rounded-lg border border-border/60 bg-card">
      <div
        className={`flex items-center gap-2.5 px-4 py-3 ${collapsible ? 'cursor-pointer hover:bg-card-2/50' : ''}`}
        onClick={() => collapsible && setOpen((o) => !o)}
      >
        {collapsible && (
          <ChevronRight size={14} className={`flex-shrink-0 text-muted transition-transform duration-150 ${isOpen ? 'rotate-90' : ''}`} />
        )}
        {icon && <span className="flex-shrink-0 text-muted">{icon}</span>}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-semibold text-txt">{title}</h3>
            {badge && <span className="rounded bg-card-2 px-1.5 py-0.5 text-xs tabular-nums text-muted">{badge}</span>}
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
      {isOpen && <div className="border-t border-border/60 px-4 py-4">{children}</div>}
    </div>
  );
}

// ---- StatusCard (themed dashboard banner) ----
const toneStyles: Record<'ok' | 'warn' | 'danger' | 'info', string> = {
  ok: 'border-good/30 bg-good/5',
  warn: 'border-warn/30 bg-warn/5',
  danger: 'border-bad/30 bg-bad/5',
  info: 'border-border bg-card-2/50',
};

export function StatusCard({
  tone,
  icon,
  title,
  children,
  action,
}: {
  tone: 'ok' | 'warn' | 'danger' | 'info';
  icon?: ReactNode;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className={`flex items-center gap-3 rounded-lg border p-3.5 ${toneStyles[tone]}`}>
      {icon && <div className="flex-shrink-0">{icon}</div>}
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium text-txt">{title}</div>
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
  icon?: ReactNode;
  label: string;
  count: number;
  hint?: string;
  to: string;
}) {
  const navigate = useNavigate();
  return (
    <div
      className="flex cursor-pointer items-center gap-3 rounded-md px-3 py-2.5 transition-colors hover:bg-card-2"
      onClick={() => navigate(to)}
    >
      <div className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md bg-card-2 text-muted">
        {icon}
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-sm text-txt">{label}</div>
        {hint && <div className="text-xs text-muted">{hint}</div>}
      </div>
      <span className="flex-shrink-0 rounded-full bg-card-2 px-2 py-0.5 text-xs font-semibold tabular-nums text-txt">{count}</span>
      <ChevronRight size={14} className="flex-shrink-0 text-muted" />
    </div>
  );
}

// ---- ActivityFeed (vertical timeline) ----
export interface ActivityItem {
  id: string | number;
  icon: ReactNode;
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
              <div className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md bg-card-2 text-xs text-muted">
                {it.icon}
              </div>
              {!last && <div className="my-1 w-px flex-1 bg-border" />}
            </div>
            <div className={`flex min-w-0 flex-1 ${last ? 'pb-1' : 'pb-4'}`}>
              <div className="min-w-0 flex-1">
                <div className="text-sm text-txt">{it.title}</div>
                {it.detail && <div className="mt-0.5 text-xs text-muted">{it.detail}</div>}
              </div>
              <div className="ml-2 flex-shrink-0 text-xs tabular-nums text-muted">{relativeTime(it.time)}</div>
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
  icon?: ReactNode;
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
              className={`flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm transition-colors ${
                active
                  ? 'border-txt font-medium text-txt'
                  : 'border-transparent text-muted hover:text-txt'
              }`}
            >
              {t.icon}
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
  icon?: ReactNode;
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
          <MoreHorizontal size={16} />
        </button>
      )}
      {open && (
        <div
          className={`absolute z-50 mt-1 min-w-[160px] rounded-lg border border-border bg-card py-1 shadow-md ${align === 'right' ? 'right-0' : 'left-0'}`}
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
                  className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm transition-colors hover:bg-card-2 disabled:cursor-not-allowed disabled:opacity-50 ${
                    it.danger ? 'text-bad' : 'text-txt'
                  }`}
                >
                  {it.icon && <span className="flex-shrink-0">{it.icon}</span>}
                  {it.label}
                </button>
              ))
            : items}
        </div>
      )}
    </div>
  );
}
