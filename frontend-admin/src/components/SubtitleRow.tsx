import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { diffSubtitles, type CueDiff } from '../lib/subtitleDiff';

/**
 * Shared, self-contained subtitle row with an expandable content preview.
 *
 * Extracted from SubtitleDrawer.tsx (its original home) so the SubtitleQueue
 * page can reuse the exact same "click to expand → lazy-load VTT → render"
 * pattern without duplicating logic. Two callers:
 *   - SubtitleDrawer: passes onDelete so the row shows a 删除 button.
 *   - SubtitleQueue: omits onDelete (read-only view of a completed job's output).
 *
 * Version toggle + diff view (2026-07-21): when source === 'llm_optimized',
 * the expanded area adds a 3-way toggle:
 *   - 润色版: the current VttContent (what downstream AI / the player sees).
 *   - 原始版: RawVttContent (the pre-polish whisper snapshot).
 *   - 对比: a structured diff — only the changed cues are listed, each with a
 *     line-level background tint (red raw / green polished) AND a token-level
 *     inline color block showing exactly which chars the LLM added/removed.
 *
 * The diff is the primary audit tool for polish quality now that the upstream
 * validation has been relaxed (2026-07-21): we trust the LLM by default and
 * let humans spot-check via this view. High-edit-distance warnings from the
 * polish job detail point the admin here.
 */
export function SubtitleRow({
  id,
  language,
  label,
  source,
  optimized,
  onDelete,
}: {
  id: number;
  language: string;
  label: string;
  source?: string;
  optimized?: boolean;
  onDelete?: () => void;
}) {
  const [open, setOpen] = useState(false);
  // The version toggle is only relevant when there's a meaningful "original"
  // to compare against — i.e. source === 'llm_optimized'. We default to
  // 'compare' in that case (the most useful view for audit); plain whispers
  // have no toggle and just render VttContent.
  const isPolished = source === 'llm_optimized' || optimized === true;
  const [view, setView] = useState<'compare' | 'polished' | 'raw'>('compare');

  // Fetch the VTT content only when the row is first expanded (lazy), and cache
  // it so re-opening is instant. The list endpoint omits vtt_content on purpose.
  const contentQ = useQuery({
    queryKey: ['subtitle-content', id],
    queryFn: () => api.getSubtitle(id),
    enabled: open,
    staleTime: Infinity,
  });
  const badge = sourceBadge(isPolished ? 'llm_optimized' : source);
  const data = contentQ.data;

  return (
    <div className="rounded-lg border border-border bg-card-2 px-3 py-2 text-sm">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="rounded bg-good/20 px-1.5 py-0.5 text-xs text-good">{language}</span>
          <strong className="text-txt">{label}</strong>
          {badge && <span className={`rounded px-1.5 py-0.5 text-[10px] ${badge.cls}`}>{badge.text}</span>}
        </div>
        <div className="flex gap-1.5">
          <button className="btn-ghost btn-sm" onClick={() => setOpen((v) => !v)}>
            {open ? '收起' : '查看'}
          </button>
          {onDelete && <button className="btn-danger btn-sm" onClick={onDelete}>删除</button>}
        </div>
      </div>
      {open && (
        <div className="mt-2">
          {contentQ.isLoading ? (
            <div className="py-4 text-center text-xs text-muted">加载中…</div>
          ) : contentQ.isError ? (
            <div className="py-4 text-center text-xs text-bad">加载失败</div>
          ) : isPolished && data?.raw_vtt_content ? (
            <>
              <div className="mb-2 flex items-center gap-1">
                {(['compare', 'polished', 'raw'] as const).map((v) => (
                  <button
                    key={v}
                    onClick={() => setView(v)}
                    className={`rounded px-2 py-1 text-[11px] transition-colors ${
                      view === v ? 'bg-txt text-bg font-medium' : 'text-muted hover:bg-card hover:text-txt'
                    }`}
                  >
                    {v === 'compare' ? '对比' : v === 'polished' ? '润色版' : '原始版'}
                  </button>
                ))}
              </div>
              {view === 'compare' ? (
                <CompareView rawVtt={data!.raw_vtt_content} polishedVtt={data!.vtt_content} />
              ) : (
                <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded bg-black/5 p-2 text-xs leading-relaxed text-txt">
                  {view === 'polished' ? data!.vtt_content : data!.raw_vtt_content || '(空)'}
                </pre>
              )}
            </>
          ) : (
            <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded bg-black/5 p-2 text-xs leading-relaxed text-txt">
              {data?.vtt_content || '(空)'}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * CompareView renders the structured diff between raw and polished VTT. Only
 * changed cues are shown (unchanged cues would just be noise). Each changed
 * cue shows: index + timestamp header, the raw line (red tint), the polished
 * line (green tint), and below them a token-level inline diff with character
 * +/- coloring.
 *
 * If the diff is empty (polish made no changes, e.g. a re-polish that found
 * nothing new), we show a friendly "no changes" message instead of an empty
 * list — the admin opened this view expecting to see something.
 */
function CompareView({ rawVtt, polishedVtt }: { rawVtt: string; polishedVtt: string }) {
  const diffs: CueDiff[] = diffSubtitles(rawVtt, polishedVtt);
  if (diffs.length === 0) {
    return (
      <div className="rounded bg-black/5 p-4 text-center text-xs text-muted">
        润色未修改任何条目（两版字幕完全一致）
      </div>
    );
  }
  return (
    <div className="space-y-2">
      <div className="text-[11px] text-muted">
        共 <span className="font-medium text-txt">{diffs.length}</span> 处修改
      </div>
      <div className="max-h-80 space-y-2 overflow-auto">
        {diffs.map((d) => (
          <div key={d.index} className="rounded border border-border/60 bg-card p-2">
            <div className="mb-1 flex items-center justify-between text-[10px] text-muted">
              <span>#{d.index}</span>
              <span className="font-mono">{d.start} → {d.end}</span>
            </div>
            {/* Line-level tint: raw (red bg) above, polished (green bg) below.
                These give a glance-able "what changed" signal before the admin
                drills into the token-level inline diff. */}
            <div className="rounded bg-bad/10 px-2 py-1 text-xs text-bad line-through decoration-bad/40">
              {d.rawText}
            </div>
            <div className="mt-0.5 rounded bg-good/10 px-2 py-1 text-xs text-good">
              {d.polishedText}
            </div>
            {/* Token-level inline diff: each run colored by type so the admin
                can see EXACTLY which characters the LLM changed within the
                cue. This is where a homophone fix (single-char swap) vs a
                rewrite (many chars) is visually obvious. */}
            <div className="mt-1 px-2 py-1 text-xs leading-relaxed">
              {d.tokens.map((t, i) => {
                if (t.type === 'same') return <span key={i} className="text-muted">{t.text}</span>;
                if (t.type === 'add')
                  return (
                    <span key={i} className="rounded bg-good/30 px-0.5 text-good font-medium">{t.text}</span>
                  );
                return (
                  <span key={i} className="rounded bg-bad/30 px-0.5 text-bad font-medium line-through">{t.text}</span>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

/** Source badge label for a subtitle's origin. Empty/whisper → none (default). */
function sourceBadge(source?: string): { text: string; cls: string } | null {
  switch (source) {
    case 'embedded':
      return { text: '内嵌', cls: 'bg-primary/15 text-primary' };
    case 'manual':
      return { text: '手动', cls: 'bg-card-2 text-muted' };
    case 'llm_optimized':
      return { text: '已润色', cls: 'bg-good/20 text-good' };
    default:
      return null; // whisper — the default, no badge needed
  }
}
