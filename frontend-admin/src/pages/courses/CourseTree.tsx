import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { Chapter, Course, Episode } from '../../lib/types';
import { LoadingState, EmptyState } from '../../components/ui';
import { codecLabel, formatDuration, formatFileSize, resolutionLabel } from '../../lib/format';
import { useToast, useConfirm } from '../../lib/toast';
import { EpisodeEditor } from './EpisodeEditor';
import { ChapterEditor } from './ChapterEditor';
import { SubtitleDrawer } from './SubtitleDrawer';

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

  const selectedIds = Array.from(selected);

  const grouped = useMemo(() => {
    // Episodes grouped by chapterId
    const byChapter = new Map<number, Episode[]>();
    for (const ep of episodes) {
      const arr = byChapter.get(ep.chapter_id) ?? [];
      arr.push(ep);
      byChapter.set(ep.chapter_id, arr);
    }
    return byChapter;
  }, [episodes]);

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
    <div className="border-t border-border bg-card p-4">
      {/* Bulk toolbar */}
      {selectedIds.length > 0 && (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-primary/30 bg-primary/10 p-2.5">
          <span className="text-sm text-primary">已选中 {selectedIds.length} 个课时</span>
          <div className="flex items-center gap-2">
            <select className="input !py-1 !text-xs max-w-[180px]" defaultValue="" onChange={(e) => e.target.value && onBulkMove(Number(e.target.value))}>
              <option value="">移到章节...</option>
              <option value="0">默认/未分类</option>
              {chapters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.title}
                </option>
              ))}
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

      {/* Tree header */}
      <div className="mb-3 flex items-center justify-between">
        <div className="flex items-center gap-3">
          <label className="flex items-center gap-1.5 text-xs text-muted">
            <input type="checkbox" className="h-3.5 w-3.5 accent-primary" checked={selected.size > 0 && selected.size === episodes.length} onChange={selectAllInCourse} />
            全选
          </label>
          <span className="text-xs text-muted">
            {chapters.length} 章节 · {episodes.length} 课时
          </span>
        </div>
        <div className="flex gap-2">
          <button className="btn-secondary btn-sm" onClick={() => probeMissingMut.mutate()} title="探测缺失时长/封面的课时">
            📡 探测缺失时长
          </button>
          <button className="btn-secondary btn-sm" onClick={() => setAddingChapter(true)}>
            + 章节
          </button>
          <button className="btn-secondary btn-sm" onClick={() => setAddingEpisode({ chapterId: 0 })}>
            + 课时
          </button>
        </div>
      </div>

      {episodes.length === 0 && chapters.length === 0 ? (
        <EmptyState icon="🎬" title="暂无章节与课时" hint="先添加章节，或直接添加课时到默认分类。" />
      ) : (
        <div className="space-y-3">
          {/* Render chapters with their episodes */}
          {chapters.map((ch) => {
            const chEps = grouped.get(ch.id) ?? [];
            const collapsed = collapsedChapters.has(ch.id);
            return (
              <div key={ch.id} className="rounded-xl border border-primary/20 bg-primary/[0.06] p-3">
                <div className="flex items-center justify-between">
                  <button className="flex items-center gap-2 text-left" onClick={() => toggleChapterCollapse(ch.id)}>
                    <span className="text-xs text-muted">{collapsed ? '▶' : '▼'}</span>
                    <span>📁</span>
                    <strong className="text-primary">{ch.title}</strong>
                    {ch.description && <span className="text-xs text-muted">— {ch.description}</span>}
                    <span className="rounded bg-primary/20 px-1.5 py-0.5 text-[10px] text-primary">{chEps.length} 课时</span>
                  </button>
                  <div className="flex gap-1.5">
                    <button className="btn-ghost btn-sm" onClick={() => setAddingEpisode({ chapterId: ch.id })} title="在此章节添加课时">
                      + 课时
                    </button>
                    <button className="btn-ghost btn-sm" onClick={() => setEditingChapter(ch)}>
                      编辑
                    </button>
                    <button className="btn-danger btn-sm" onClick={() => onDeleteChapter(ch)}>
                      删除
                    </button>
                  </div>
                </div>
                {!collapsed && (
                  <div className="mt-2 space-y-1.5 border-l border-dashed border-border pl-3">
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
              <div className="rounded-xl border border-muted/20 bg-muted/[0.06] p-3">
                <div className="mb-2 flex items-center gap-2">
                  <span>📂</span>
                  <strong className="text-muted">默认/未分类</strong>
                  <span className="rounded bg-muted/20 px-1.5 py-0.5 text-[10px] text-muted">{unc.length} 课时</span>
                </div>
                <div className="space-y-1.5">
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

      {subtitleFor && (
        <SubtitleDrawer episode={subtitleFor} onClose={() => setSubtitleFor(null)} />
      )}
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
  return (
    <div className="group flex items-center gap-2.5 rounded-lg border border-border bg-card-2 px-3 py-2 text-sm transition hover:border-primary/40">
      <input type="checkbox" checked={selected} onChange={onToggle} className="h-3.5 w-3.5 flex-shrink-0 accent-primary" />

      {/* Sort controls */}
      <div className="flex flex-shrink-0 flex-col text-[9px] leading-none">
        <button onClick={onMoveUp} disabled={!onMoveUp} className="text-primary disabled:opacity-30 hover:opacity-100">
          ▲
        </button>
        <button onClick={onMoveDown} disabled={!onMoveDown} className="text-primary disabled:opacity-30 hover:opacity-100">
          ▼
        </button>
      </div>

      <span className="w-9 flex-shrink-0 font-bold text-primary">P{ep.sort_order}</span>

      {/* Cover thumbnail */}
      <div className="h-10 w-14 flex-shrink-0 overflow-hidden rounded bg-black/30">
        {ep.cover_url ? (
          <img src={ep.cover_url} alt="" className="h-full w-full object-cover" onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')} />
        ) : (
          <div className="flex h-full w-full items-center justify-center text-xs opacity-30">🎬</div>
        )}
      </div>

      {/* Title + path */}
      <div className="min-w-0 flex-1">
        <div className="truncate font-medium text-txt">{ep.title}</div>
        <div className="truncate font-mono text-[11px] text-muted" title={ep.video_relative_path}>
          {ep.video_relative_path}
        </div>
      </div>

      {/* Metadata badges */}
      <div className="hidden flex-shrink-0 items-center gap-1.5 md:flex">
        {ep.duration_seconds ? (
          <span className="rounded bg-warn/10 px-1.5 py-0.5 text-[11px] text-warn" title="视频时长">
            ⏱ {formatDuration(ep.duration_seconds)}
          </span>
        ) : (
          <span className="rounded bg-bad/10 px-1.5 py-0.5 text-[11px] text-bad" title="尚未探测时长">
            无时长
          </span>
        )}
        {meta && (resolutionLabel(meta.width, meta.height) || codecLabel(meta.video_codec)) && (
          <span className="rounded bg-primary/10 px-1.5 py-0.5 text-[11px] text-primary">
            {resolutionLabel(meta.width, meta.height)}
            {meta.video_codec ? `·${codecLabel(meta.video_codec)}` : ''}
          </span>
        )}
        {ep.file_size && <span className="text-[11px] text-muted">{formatFileSize(ep.file_size)}</span>}
        {ep.file_hash ? (
          <span className="rounded bg-good/10 px-1.5 py-0.5 text-[11px] text-good" title={`SHA: ${ep.file_hash}`}>
            ✓
          </span>
        ) : null}
      </div>

      {/* Actions */}
      <div className="flex flex-shrink-0 gap-1">
        <button className="btn-ghost btn-sm" onClick={onSubtitles} title="字幕管理">
          💬
        </button>
        <button className="btn-ghost btn-sm" onClick={onEdit}>
          编辑
        </button>
        <button className="btn-danger btn-sm" onClick={onDelete}>
          删除
        </button>
      </div>
    </div>
  );
}
