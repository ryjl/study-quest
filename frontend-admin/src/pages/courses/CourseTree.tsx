import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { Chapter, Course, Episode } from '../../lib/types';
import { LoadingState, EmptyState } from '../../components/ui';
import { codecLabel, formatDuration, formatFileSize, resolutionLabel } from '../../lib/format';
import { useToast, useConfirm } from '../../lib/toast';
import { sortBy, timeValue, type SortDir, type SortOption } from '../../lib/sort';
import { EpisodeEditor } from './EpisodeEditor';
import { ChapterEditor } from './ChapterEditor';
import { SubtitleDrawer } from './SubtitleDrawer';
import {
  ChevronRight,
  ChevronUp,
  ChevronDown,
  Folder,
  FolderOpen,
  FolderPlus,
  Plus,
  Pencil,
  Trash2,
  Captions,
  Radio,
  ArrowUp,
  ArrowDown,
  Film,
  Clock,
} from 'lucide-react';

// Display-sort options for episodes WITHIN a chapter (or the uncategorized
// bucket). "Apply as order" persists the displayed sequence via the existing
// reorder endpoint; pure display otherwise.
const EPISODE_SORT_OPTIONS: SortOption<Episode>[] = [
  { key: 'manual', label: '手动顺序', value: (e) => e.sort_order },
  { key: 'title', label: '标题', value: (e) => e.title },
  { key: 'path', label: '文件名/路径', value: (e) => e.video_relative_path },
  { key: 'duration', label: '时长', value: (e) => e.duration_seconds ?? 0 },
  { key: 'size', label: '文件大小', value: (e) => e.file_size ?? 0 },
  { key: 'updated', label: '修改时间', value: (e) => timeValue(e.updated_at) },
  { key: 'created', label: '创建时间', value: (e) => timeValue(e.created_at) },
];

export function CourseTree({ course, onChanged }: { course: Course; onChanged: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();

  const epsQ = useQuery({ queryKey: ['episodes', course.id], queryFn: () => api.listEpisodes(course.id) });
  const chsQ = useQuery({ queryKey: ['chapters', course.id], queryFn: () => api.listChapters(course.id) });

  const episodes = epsQ.data ?? [];
  const chapters = chsQ.data ?? [];

  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [collapsedChapters, setCollapsedChapters] = useState<Set<number>>(new Set());
  const [editingEpisode, setEditingEpisode] = useState<Episode | null>(null);
  const [addingEpisode, setAddingEpisode] = useState<{ chapterId: number } | null>(null);
  const [editingChapter, setEditingChapter] = useState<Chapter | null>(null);
  const [addingChapter, setAddingChapter] = useState(false);
  const [subtitleFor, setSubtitleFor] = useState<Episode | null>(null);
  // Episode display-sort (applies to every chapter + the uncategorized bucket).
  const [epSortKey, setEpSortKey] = useState('manual');
  const [epSortDir, setEpSortDir] = useState<SortDir>('asc');

  const selectedIds = Array.from(selected);

  const grouped = useMemo(() => {
    // Episodes grouped by chapterId, each group sorted by the active display
    // sort (manual = native sort_order; the rest re-order for display only).
    const opt = EPISODE_SORT_OPTIONS.find((o) => o.key === epSortKey) ?? EPISODE_SORT_OPTIONS[0];
    const byChapter = new Map<number, Episode[]>();
    for (const ep of episodes) {
      const arr = byChapter.get(ep.chapter_id) ?? [];
      arr.push(ep);
      byChapter.set(ep.chapter_id, arr);
    }
    // Preserve manual order when epSortKey==='manual' (episodes already come
    // back ordered by sort_order from the repo). Only re-sort for other keys.
    if (epSortKey !== 'manual') {
      for (const [k, arr] of byChapter) {
        byChapter.set(k, sortBy(arr, opt, epSortDir));
      }
    }
    return byChapter;
  }, [episodes, epSortKey, epSortDir]);

  const invalidateAll = () => {
    qc.invalidateQueries({ queryKey: ['episodes', course.id] });
    qc.invalidateQueries({ queryKey: ['chapters', course.id] });
    qc.invalidateQueries({ queryKey: ['courses'] });
    onChanged();
  };

  const toggleSelect = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const selectAllInCourse = () => {
    if (selected.size === episodes.length) setSelected(new Set());
    else setSelected(new Set(episodes.map((e) => e.id)));
  };

  // Mutations
  const delEpMut = useMutation({
    mutationFn: api.deleteEpisode,
    onSuccess: () => {
      toast.success('课时已删除');
      invalidateAll();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const delChMut = useMutation({
    mutationFn: api.deleteChapter,
    onSuccess: () => {
      toast.success('章节已删除');
      invalidateAll();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const bulkMoveMut = useMutation({
    mutationFn: ({ ids, chapterId }: { ids: number[]; chapterId: number }) => api.bulkMoveEpisodes(ids, chapterId),
    onSuccess: () => {
      toast.success('批量移动完成');
      setSelected(new Set());
      invalidateAll();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const bulkDeleteMut = useMutation({
    mutationFn: api.bulkDeleteEpisodes,
    onSuccess: () => {
      toast.success('批量删除完成');
      setSelected(new Set());
      invalidateAll();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const onDeleteEpisode = async (ep: Episode) => {
    const ok = await confirm({ message: `删除课时「${ep.title}」？`, detail: '将一并删除该课时的进度与字幕记录。', danger: true });
    if (ok) delEpMut.mutate(ep.id);
  };

  const onDeleteChapter = async (ch: Chapter) => {
    const ok = await confirm({
      message: `删除章节「${ch.title}」？`,
      detail: '章节下的课时不会被删除，会自动归类为「默认/未分类」。',
      danger: true,
    });
    if (ok) delChMut.mutate(ch.id);
  };

  const onBulkDelete = async () => {
    const ok = await confirm({
      message: `批量删除 ${selectedIds.length} 个课时？`,
      detail: '该操作不可撤销，将永久删除课时及其进度/字幕。',
      danger: true,
    });
    if (ok) bulkDeleteMut.mutate(selectedIds);
  };

  const onBulkMove = (chapterId: number) => bulkMoveMut.mutate({ ids: selectedIds, chapterId });

  const probeMissingMut = useMutation({
    mutationFn: api.scanMissingDurations,
    onSuccess: (d) => toast.info(d.message),
    onError: (e) => toast.error((e as Error).message),
  });

  // Human-readable labels for the machine skip-reason codes the backend
  // returns from a batch enqueue, so the toast explains *why* episodes were
  // skipped rather than just a count.
  const skipReasonLabel: Record<string, string> = {
    has_subtitle: '已有字幕',
    already_queued: '已在队列',
    entertainment: '娱乐内容',
    not_found: '不存在',
  };
  const enqueueSubtitlesMut = useMutation({
    mutationFn: (vars: { ids: number[]; priority: number }) => api.enqueueSubtitleJobs(vars.ids, vars.priority),
    onSuccess: (d) => {
      const added = d.enqueued.length;
      const skipped = d.skipped.length;
      if (added > 0 && skipped === 0) {
        toast.success(`已加入字幕队列：${added} 个`);
      } else if (added > 0 && skipped > 0) {
        // Summarize skip reasons so the operator knows why some were left out.
        const reasonCounts: Record<string, number> = {};
        for (const id of d.skipped) {
          const r = skipReasonLabel[d.reasons[id]] ?? d.reasons[id] ?? '跳过';
          reasonCounts[r] = (reasonCounts[r] ?? 0) + 1;
        }
        const summary = Object.entries(reasonCounts).map(([r, n]) => `${n} ${r}`).join('，');
        toast.success(`已加入 ${added} 个；跳过 ${skipped} 个（${summary}）`);
      } else {
        toast.info('没有新的课时加入队列');
      }
      setSelected(new Set());
      invalidateAll();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const onEnqueueSubtitles = (priority: number) => enqueueSubtitlesMut.mutate({ ids: selectedIds, priority });

  const moveEpisode = async (ep: Episode, dir: -1 | 1) => {
    const siblings = grouped.get(ep.chapter_id) ?? [];
    const idx = siblings.findIndex((e) => e.id === ep.id);
    const target = idx + dir;
    if (target < 0 || target >= siblings.length) return;
    const reordered = siblings.slice();
    [reordered[idx], reordered[target]] = [reordered[target], reordered[idx]];
    try {
      await api.reorderEpisodes(reordered.map((e) => e.id));
      qc.invalidateQueries({ queryKey: ['episodes', course.id] });
    } catch (e) {
      toast.error('排序失败: ' + (e as Error).message);
    }
  };

  // applyDisplaySortAsOrder writes the currently-displayed order back to
  // sort_order (via the existing reorder endpoint), so a display sort can be
  // made permanent. The reorder endpoint rewrites sort_order for the given ids
  // in array order; we feed it every episode id in display order so the whole
  // course converges to the shown sequence.
  const applyDisplaySortAsOrder = async () => {
    // Flatten grouped map in a stable order: chapters first (their order), then
    // the uncategorized bucket (chapter_id === 0) last.
    const orderedIds: number[] = [];
    for (const ch of chapters) {
      orderedIds.push(...(grouped.get(ch.id) ?? []).map((e) => e.id));
    }
    orderedIds.push(...(grouped.get(0) ?? []).map((e) => e.id));
    if (orderedIds.length === 0) return;
    try {
      await api.reorderEpisodes(orderedIds);
      toast.success('已将当前排序保存为课时顺序');
      setEpSortKey('manual');
      qc.invalidateQueries({ queryKey: ['episodes', course.id] });
    } catch (e) {
      toast.error('保存排序失败: ' + (e as Error).message);
    }
  };

  const toggleChapterCollapse = (id: number) => {
    setCollapsedChapters((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  if (epsQ.isLoading || chsQ.isLoading) return <LoadingState />;

  return (
    <div className="border-t border-border/60 bg-card px-4 py-3">
      {/* Bulk toolbar — appears only when episodes are selected. Neutral
          palette (was violet-tinted); reads as "active state" via bg alone. */}
      {selectedIds.length > 0 && (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-border bg-card-2 px-3 py-2">
          <span className="text-sm font-medium text-txt">已选中 {selectedIds.length} 个课时</span>
          <div className="ml-auto flex flex-shrink-0 flex-wrap items-center gap-2">
            <select className="input !w-auto !py-1 !text-xs" defaultValue="" onChange={(e) => e.target.value && onBulkMove(Number(e.target.value))}>
              <option value="">移到章节...</option>
              <option value="0">默认/未分类</option>
              {chapters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.title}
                </option>
              ))}
            </select>
            {/* Subtitle queue: opt the selected episodes into whisper transcription.
                Priority is chosen inline (higher = the worker picks it first); the
                default 0 is fine for most batches. */}
            <select
              className="input !w-auto !py-1 !text-xs"
              defaultValue=""
              onChange={(e) => {
                const v = e.target.value;
                if (v !== '') onEnqueueSubtitles(Number(v));
                e.target.value = '';
              }}
              disabled={enqueueSubtitlesMut.isPending}
              title="把选中课时加入字幕生成队列"
            >
              <option value="">加入字幕队列...</option>
              <option value="0">普通优先级 (0)</option>
              <option value="1">优先 (1)</option>
              <option value="2">较优先 (2)</option>
              <option value="3">最优先 (3)</option>
            </select>
            <button className="btn-danger btn-sm" onClick={onBulkDelete}>
              批量删除
            </button>
            <button className="btn-ghost btn-sm" onClick={() => setSelected(new Set())}>
              取消
            </button>
          </div>
        </div>
      )}

      {/* Tree header — streamlined to high-frequency controls only:
          select-all + counts on the left, sort on the right. Low-frequency
          add/probe actions moved into the ⋯ menu below. */}
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-shrink-0 items-center gap-3">
          <label className="flex items-center gap-1.5 text-xs text-muted">
            <input type="checkbox" className="h-3.5 w-3.5 accent-primary" checked={selected.size > 0 && selected.size === episodes.length} onChange={selectAllInCourse} />
            全选
          </label>
          <span className="text-xs tabular-nums text-muted">
            {chapters.length} 章节 · {episodes.length} 课时
          </span>
        </div>
        <div className="flex flex-shrink-0 items-center gap-2">
          <span className="text-xs text-muted">排序</span>
          <select
            className="input !w-auto !py-1 !text-xs"
            value={epSortKey}
            onChange={(e) => setEpSortKey(e.target.value)}
          >
            {EPISODE_SORT_OPTIONS.map((o) => (
              <option key={o.key} value={o.key}>
                {o.label}
              </option>
            ))}
          </select>
          <button
            className="btn-ghost btn-sm"
            onClick={() => setEpSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))}
            title={epSortDir === 'asc' ? '当前：正序' : '当前：倒序'}
          >
            {epSortDir === 'asc' ? <ArrowUp size={14} /> : <ArrowDown size={14} />}
          </button>
          {epSortKey !== 'manual' && (
            <button className="btn-secondary btn-sm" onClick={applyDisplaySortAsOrder} title="把当前显示顺序保存为课时顺序">
              保存为顺序
            </button>
          )}
          <div className="mx-1 h-4 w-px bg-border" />
          {/* Add chapter/episode stay as persistent buttons (high-frequency);
              probe-missing collapses into the ⋯ menu (low-frequency maintenance). */}
          <button className="btn-secondary btn-sm" onClick={() => setAddingChapter(true)} title="添加章节">
            <FolderPlus size={14} /> 章节
          </button>
          <button className="btn-secondary btn-sm" onClick={() => setAddingEpisode({ chapterId: 0 })} title="添加课时到默认分类">
            <Plus size={14} /> 课时
          </button>
          {/* 低频维护操作:直接降级成按钮(原三点菜单只有这一项,没必要藏)。 */}
          <button className="btn-secondary btn-sm" onClick={() => probeMissingMut.mutate()} title="扫描所有课时,补全缺失的视频时长信息">
            <Radio size={14} /> 探测时长
          </button>
        </div>
      </div>

      {episodes.length === 0 && chapters.length === 0 ? (
        <EmptyState icon={<Film size={28} />} title="暂无章节与课时" hint="使用上方「章节」「课时」按钮添加，或直接添加课时到默认分类。" />
      ) : (
        <div className="space-y-2">
          {/* Render chapters with their episodes */}
          {chapters.map((ch) => {
            const chEps = grouped.get(ch.id) ?? [];
            const collapsed = collapsedChapters.has(ch.id);
            return (
              <div key={ch.id} className="rounded-md border border-border bg-card-2/40 p-2.5">
                <div className="flex items-center justify-between gap-2">
                  <button className="flex min-w-0 flex-1 items-center gap-2 text-left" onClick={() => toggleChapterCollapse(ch.id)}>
                    <ChevronRight size={12} className={`flex-shrink-0 text-muted transition-transform ${collapsed ? '' : 'rotate-90'}`} />
                    <Folder size={14} className="flex-shrink-0 text-muted" />
                    <strong className="truncate text-sm font-medium text-txt">{ch.title}</strong>
                    {ch.description && <span className="truncate text-xs text-muted">— {ch.description}</span>}
                    <span className="flex-shrink-0 rounded bg-card-2 px-1.5 py-0.5 text-[10px] tabular-nums text-muted">{chEps.length}</span>
                  </button>
                  <div className="flex flex-shrink-0 gap-0.5">
                    <button className="btn-ghost btn-sm !px-1.5" onClick={() => setAddingEpisode({ chapterId: ch.id })} title="在此章节添加课时">
                      <Plus size={14} />
                    </button>
                    <button className="btn-ghost btn-sm !px-1.5" onClick={() => setEditingChapter(ch)} title="编辑章节">
                      <Pencil size={14} />
                    </button>
                    <button className="btn-ghost btn-sm !px-1.5 text-bad hover:text-bad" onClick={() => onDeleteChapter(ch)} title="删除章节">
                      <Trash2 size={14} />
                    </button>
                  </div>
                </div>
                {!collapsed && (
                  <div className="mt-1.5 space-y-1 border-l border-border pl-2.5" style={{ marginLeft: '0.875rem' }}>
                    {chEps.length === 0 ? (
                      <div className="py-2 text-xs italic text-muted">(该章节下暂无课时)</div>
                    ) : (
                      chEps.map((ep, i) => (
                        <EpisodeRow
                          key={ep.id}
                          ep={ep}
                          selected={selected.has(ep.id)}
                          onToggle={() => toggleSelect(ep.id)}
                          onEdit={() => setEditingEpisode(ep)}
                          onSubtitles={() => setSubtitleFor(ep)}
                          onDelete={() => onDeleteEpisode(ep)}
                          onMoveUp={i > 0 ? () => moveEpisode(ep, -1) : undefined}
                          onMoveDown={i < chEps.length - 1 ? () => moveEpisode(ep, 1) : undefined}
                        />
                      ))
                    )}
                  </div>
                )}
              </div>
            );
          })}

          {/* Uncategorized episodes */}
          {(() => {
            const unc = grouped.get(0) ?? [];
            if (unc.length === 0) return null;
            return (
              <div className="rounded-md border border-dashed border-border bg-card-2/20 p-2.5">
                <div className="mb-1.5 flex items-center gap-2">
                  <FolderOpen size={14} className="text-muted" />
                  <strong className="text-sm text-muted">默认/未分类</strong>
                  <span className="rounded bg-card-2 px-1.5 py-0.5 text-[10px] tabular-nums text-muted">{unc.length}</span>
                </div>
                <div className="space-y-1">
                  {unc.map((ep, i) => (
                    <EpisodeRow
                      key={ep.id}
                      ep={ep}
                      selected={selected.has(ep.id)}
                      onToggle={() => toggleSelect(ep.id)}
                      onEdit={() => setEditingEpisode(ep)}
                      onSubtitles={() => setSubtitleFor(ep)}
                      onDelete={() => onDeleteEpisode(ep)}
                      onMoveUp={i > 0 ? () => moveEpisode(ep, -1) : undefined}
                      onMoveDown={i < unc.length - 1 ? () => moveEpisode(ep, 1) : undefined}
                    />
                  ))}
                </div>
              </div>
            );
          })()}
        </div>
      )}

      {/* Editors */}
      {(editingEpisode || addingEpisode) && (
        <EpisodeEditor
          courseId={course.id}
          episode={editingEpisode}
          chapters={chapters}
          defaultChapterId={addingEpisode?.chapterId ?? 0}
          onClose={() => {
            setEditingEpisode(null);
            setAddingEpisode(null);
          }}
          onSaved={() => {
            setEditingEpisode(null);
            setAddingEpisode(null);
            invalidateAll();
          }}
        />
      )}

      {(editingChapter || addingChapter) && (
        <ChapterEditor
          courseId={course.id}
          chapter={editingChapter}
          onClose={() => {
            setEditingChapter(null);
            setAddingChapter(false);
          }}
          onSaved={() => {
            setEditingChapter(null);
            setAddingChapter(false);
            invalidateAll();
          }}
        />
      )}

      {subtitleFor && <SubtitleDrawer episode={subtitleFor} onClose={() => setSubtitleFor(null)} />}
    </div>
  );
}

function EpisodeRow({
  ep,
  selected,
  onToggle,
  onEdit,
  onSubtitles,
  onDelete,
  onMoveUp,
  onMoveDown,
}: {
  ep: Episode;
  selected: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onSubtitles: () => void;
  onDelete: () => void;
  onMoveUp?: () => void;
  onMoveDown?: () => void;
}) {
  const meta = ep.media_meta_json ? JSON.parse(ep.media_meta_json) : null;
  const hasDuration = ep.duration_seconds != null && ep.duration_seconds > 0;
  const resLabel = meta ? resolutionLabel(meta.width, meta.height) : '';
  const codec = meta?.video_codec ? codecLabel(meta.video_codec) : '';
  return (
    // group enables the hover-revealed action bar; actions are invisible
    // until hover so a dense episode list reads cleanly row over row.
    <div className="group flex items-center gap-2.5 rounded-md border border-border/60 bg-card px-2.5 py-1.5 text-sm transition-colors hover:border-border hover:bg-card-2/30">
      <input type="checkbox" checked={selected} onChange={onToggle} className="h-3.5 w-3.5 flex-shrink-0 accent-primary" />

      {/* Sort controls — muted, hover to full opacity. */}
      <div className="flex flex-shrink-0 flex-col opacity-40 transition-opacity group-hover:opacity-100">
        <button onClick={onMoveUp} disabled={!onMoveUp} className="text-muted hover:text-txt disabled:opacity-20" title="上移">
          <ChevronUp size={12} />
        </button>
        <button onClick={onMoveDown} disabled={!onMoveDown} className="text-muted hover:text-txt disabled:opacity-20" title="下移">
          <ChevronDown size={12} />
        </button>
      </div>

      <span className="w-7 flex-shrink-0 text-xs tabular-nums text-muted">P{ep.sort_order}</span>

      {/* Cover thumbnail */}
      <div className="h-9 w-12 flex-shrink-0 overflow-hidden rounded bg-card-2">
        {ep.cover_url ? (
          <img src={ep.cover_url} alt="" className="h-full w-full object-cover" onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')} />
        ) : (
          <div className="flex h-full w-full items-center justify-center text-muted opacity-40">
            <Film size={14} />
          </div>
        )}
      </div>

      {/* Title + path. Both wrap (break-words / break-all) instead of
          truncating with …, so long names/paths are fully readable. The title
          column is flex-1 + min-w-0 so it absorbs width changes while the
          metadata and action columns to its right hold FIXED widths — that
          fixed-width pairing is what makes every row's right side align
          vertically across the list. */}
      <div className="min-w-0 flex-1">
        <div className="break-words font-medium text-txt">{ep.title}</div>
        <div className="break-all font-mono text-[11px] text-muted" title={ep.video_relative_path}>
          {ep.video_relative_path}
        </div>
      </div>

      {/* Metadata — neutral tags, only "no duration" reads as a warning. */}
      <div className="hidden w-[200px] flex-shrink-0 items-center justify-end gap-1.5 md:flex">
        {hasDuration ? (
          <span className="inline-flex items-center gap-1 rounded bg-card-2 px-1.5 py-0.5 text-[11px] tabular-nums text-muted" title="视频时长">
            <Clock size={10} />
            {formatDuration(ep.duration_seconds!)}
          </span>
        ) : (
          <span className="rounded bg-bad/10 px-1.5 py-0.5 text-[11px] text-bad" title="尚未探测时长">
            无时长
          </span>
        )}
        {(resLabel || codec) && (
          <span className="rounded bg-card-2 px-1.5 py-0.5 text-[11px] text-muted">
            {resLabel}
            {codec ? `·${codec}` : ''}
          </span>
        )}
        {ep.file_size && <span className="text-[11px] tabular-nums text-muted">{formatFileSize(ep.file_size)}</span>}
      </div>

      {/* Actions — hover-revealed ghost icon buttons; subtitles is the
          least-frequent so it's last. Keeps each row visually quiet at rest. */}
      <div className="flex w-[96px] flex-shrink-0 justify-end gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
        <button className="btn-ghost btn-sm !px-1.5" onClick={onSubtitles} title="字幕管理">
          <Captions size={14} />
        </button>
        <button className="btn-ghost btn-sm !px-1.5" onClick={onEdit} title="编辑">
          <Pencil size={14} />
        </button>
        <button className="btn-ghost btn-sm !px-1.5 text-bad hover:text-bad" onClick={onDelete} title="删除">
          <Trash2 size={14} />
        </button>
      </div>
    </div>
  );
}
