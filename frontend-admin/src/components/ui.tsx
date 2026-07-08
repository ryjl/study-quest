import { useEffect, type ReactNode } from 'react';
import { subjectMeta, type SubjectMeta } from '../lib/types';
import { useSubjects } from '../lib/useSubjects';

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
