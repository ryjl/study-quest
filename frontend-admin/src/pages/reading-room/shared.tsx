// Shared bits used across the reading-room modals + list: form-field wrappers
// (subject / grade / tags / series selects), the generic ReadingItemRow, the
// expanded SeriesChildren loader, and the whitelist-domain parser. Extracted
// from the old monolithic ReadingRoom.tsx so each modal file is standalone.

import type { ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { ReadingArticle, ReadingBook, ReadingSeries } from '../../lib/types';
import { useSubjects } from '../../lib/useSubjects';
import { GradePicker } from '../../components/inputs';
import { TagInput } from '../../components/TagInput';
import { BookMarked, Globe } from 'lucide-react';

// Parse the stored whitelist_domains (JSON array string or comma-separated)
// into a clean comma-separated string for the text input.
export function parseWhitelistForDisplay(raw: string): string {
  if (!raw || raw === '[]') return '';
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return parsed.filter((d) => typeof d === 'string' && d).join(', ');
    }
  } catch {
    // Not JSON — treat as already comma-separated.
  }
  return raw;
}

// --- Generic item row ---

export function ReadingItemRow({
  icon,
  title,
  subtitle,
  subjectColor,
  subjectLabel,
  compact,
  onEdit,
  onDelete,
}: {
  icon: ReactNode;
  title: string;
  subtitle: string;
  subjectColor?: string;
  subjectLabel?: string;
  compact?: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <div
      className={`group flex items-center gap-2.5 rounded-lg border border-border bg-card-2 ${
        compact ? 'px-3 py-1.5' : 'px-3 py-2'
      }`}
    >
      <span className="text-muted">{icon}</span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-txt">{title}</span>
          {subjectLabel && (
            <span
              className="shrink-0 rounded-md px-1.5 py-0.5 text-xs font-medium"
              style={{ backgroundColor: `${subjectColor}20`, color: subjectColor }}
            >
              {subjectLabel}
            </span>
          )}
        </div>
        <p className="truncate text-xs text-muted">{subtitle}</p>
      </div>
      <div className="flex shrink-0 gap-1 opacity-60 transition group-hover:opacity-100">
        <button className="btn-ghost btn-sm" onClick={onEdit}>
          编辑
        </button>
        <button className="btn-ghost btn-sm text-bad hover:bg-bad/10" onClick={onDelete}>
          删除
        </button>
      </div>
    </div>
  );
}

// --- Series children (expanded view) ---
// Each instance owns its own useQuery keyed to its seriesId, so multiple
// expanded series each load + display their own children correctly.

export function SeriesChildren({
  seriesId,
  onEditBook,
  onEditArticle,
  onDeleteBook,
  onDeleteArticle,
}: {
  seriesId: number;
  onEditBook: (b: ReadingBook) => void;
  onEditArticle: (a: ReadingArticle) => void;
  onDeleteBook: (b: ReadingBook) => void;
  onDeleteArticle: (a: ReadingArticle) => void;
}) {
  const detailQ = useQuery({
    queryKey: ['reading-series-detail', seriesId],
    queryFn: () => api.getReadingSeriesDetail(seriesId),
  });
  if (detailQ.isLoading || !detailQ.data) {
    return <div className="border-t border-border p-4 text-sm text-muted">加载中...</div>;
  }
  const { books, articles } = detailQ.data;
  if (books.length === 0 && articles.length === 0) {
    return (
      <div className="border-t border-border p-4 text-sm text-muted">
        该系列还没有内容，编辑书籍/文章时可选择归入此系列。
      </div>
    );
  }
  return (
    <div className="space-y-1.5 border-t border-border p-3">
      {books.map((b) => (
        <ReadingItemRow
          key={b.id}
          icon={<BookMarked size={16} />}
          title={b.title}
          subtitle={`${b.page_count ?? '?'}页 · ${b.file_relative_path}`}
          compact
          onEdit={() => onEditBook(b)}
          onDelete={() => onDeleteBook(b)}
        />
      ))}
      {articles.map((a) => (
        <ReadingItemRow
          key={a.id}
          icon={<Globe size={16} />}
          title={a.title}
          subtitle={a.source_url}
          compact
          onEdit={() => onEditArticle(a)}
          onDelete={() => onDeleteArticle(a)}
        />
      ))}
    </div>
  );
}

// --- Shared form fields ---

export function SubjectSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const subjectsQ = useSubjects();
  return (
    <div>
      <label className="mb-1 block text-xs text-muted">科目</label>
      <select className="input" value={value} onChange={(e) => onChange(e.target.value)} required>
        <option value="">请选择科目</option>
        {(subjectsQ.data ?? []).map((s) => (
          <option key={s.key} value={s.key}>
            {s.label}
          </option>
        ))}
      </select>
    </div>
  );
}

export function GradeField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-muted">年级（可多选）</label>
      <GradePicker value={value} onChange={onChange} />
    </div>
  );
}

export function TagField({ value, onChange }: { value: number[]; onChange: (v: number[]) => void }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-muted">标签</label>
      <TagInput value={value} onChange={onChange} />
    </div>
  );
}

export function SeriesSelect({
  value,
  onChange,
  series,
}: {
  value: number;
  onChange: (v: number) => void;
  series: ReadingSeries[];
}) {
  return (
    <div>
      <label className="mb-1 block text-xs text-muted">所属系列（不选则为散本）</label>
      <select className="input" value={value} onChange={(e) => onChange(Number(e.target.value))}>
        <option value={0}>— 散本/散文（不属于任何系列）—</option>
        {series.map((s) => (
          <option key={s.id} value={s.id}>
            {s.title}
          </option>
        ))}
      </select>
    </div>
  );
}
