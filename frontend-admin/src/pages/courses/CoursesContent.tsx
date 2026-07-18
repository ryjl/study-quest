import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { GRADES, subjectMeta, type Course } from '../../lib/types';
import { useSubjects } from '../../lib/useSubjects';
import { useUnlockTemplate, strategyLabel } from '../../lib/useUnlock';
import { DropdownMenu, EmptyState, LoadingState, SubjectBadge, SubjectIcon, Tag } from '../../components/ui';
import { CourseUnlockTemplateModal } from '../../components/CourseUnlockTemplateModal';
import { formatDurationShort, relativeTime } from '../../lib/format';
import { useToast, useConfirm } from '../../lib/toast';
import { sortBy, timeValue, type SortDir, type SortOption } from '../../lib/sort';
import { CourseTree } from './CourseTree';
import {
  Search,
  ChevronDown,
  ChevronUp,
  ChevronRight,
  Clock,
  Pencil,
  Trash2,
  ArrowUp,
  ArrowDown,
  Library,
} from 'lucide-react';

// Display-sort options for the course list. Pure display — does NOT rewrite
// sort_order; "apply as order" persistence is out of scope here (the manual
// ▲/▼ controls on episodes are the persistent path).
const COURSE_SORT_OPTIONS: SortOption<Course>[] = [
  { key: 'updated', label: '更新时间', value: (c) => timeValue(c.updated_at) },
  { key: 'created', label: '创建时间', value: (c) => timeValue(c.created_at) },
  { key: 'title', label: '标题', value: (c) => c.title },
  { key: 'duration', label: '总时长', value: (c) => c.total_duration_seconds ?? 0 },
  { key: 'episodes', label: '课时数', value: (c) => c.episode_count ?? 0 },
  { key: 'subject', label: '科目', value: (c) => c.subject },
];

export function CoursesContent({ onEdit, onChanged }: { onEdit: (c: Course) => void; onChanged: () => void }) {
  const [search, setSearch] = useState('');
  const [gradeFilter, setGradeFilter] = useState('all');
  const [subjectFilter, setSubjectFilter] = useState('all');
  const [sortKey, setSortKey] = useState('updated');
  const [sortDir, setSortDir] = useState<SortDir>('desc');
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [unlockForCourse, setUnlockForCourse] = useState<Course | null>(null);
  const qc = useQueryClient();
  const toast = useToast();
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];
  const confirm = useConfirm();

  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const courses = coursesQ.data ?? [];

  const filtered = useMemo(() => {
    const f = courses.filter((c) => {
      if (gradeFilter !== 'all') {
        const grades = (c.grade || '').split(',').map((g) => g.trim());
        if (!grades.includes(gradeFilter)) return false;
      }
      if (subjectFilter !== 'all' && c.subject !== subjectFilter) return false;
      if (search.trim()) {
        const q = search.toLowerCase();
        const hay = `${c.title} ${c.subject} ${c.tags_list?.join(' ') ?? ''}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
    const opt = COURSE_SORT_OPTIONS.find((o) => o.key === sortKey) ?? COURSE_SORT_OPTIONS[0];
    return sortBy(f, opt, sortDir);
  }, [courses, gradeFilter, subjectFilter, search, sortKey, sortDir]);

  const deleteCourseMut = useMutation({
    mutationFn: (id: number) => api.deleteCourse(id),
    onSuccess: () => {
      toast.success('课程已删除');
      qc.invalidateQueries({ queryKey: ['courses'] });
      onChanged();
    },
    onError: (e) => toast.error('删除失败: ' + (e as Error).message),
  });

  const toggleExpand = (id: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const onDeleteCourse = async (c: Course) => {
    const ok = await confirm({
      message: `删除课程「${c.title}」？`,
      detail: '此操作将连带删除旗下所有章节、课时、进度和字幕记录，且不可撤销。',
      danger: true,
    });
    if (ok) deleteCourseMut.mutate(c.id);
  };

  if (coursesQ.isLoading) return <LoadingState />;
  if (coursesQ.error) return <div className="card text-bad">加载失败: {(coursesQ.error as Error).message}</div>;

  if (courses.length === 0) {
    return <EmptyState icon={<Library size={32} />} title="课程库为空" hint="点击右上角「新增课程库」创建您的第一个课程。" />;
  }

  return (
    <div>
      {/* Filter toolbar — two rows. Row 1: search (primary) + count.
          Row 2: filters + sort (secondary), wraps on narrow viewports.
          Replaces the old single cramped row that jammed 5+ controls together. */}
      <div className="mb-4 rounded-lg border border-border/60 bg-card px-3 py-2.5">
        {/* Row 1 */}
        <div className="flex items-center gap-3">
          <div className="relative max-w-md flex-1">
            <Search size={14} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-muted" />
            <input
              className="input !pl-9"
              placeholder="搜索课程名 / 标签..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <span className="ml-auto whitespace-nowrap text-xs tabular-nums text-muted">
            <span className="font-medium text-txt">{filtered.length}</span> / {courses.length} 门
          </span>
        </div>
        {/* Row 2 */}
        <div className="mt-2.5 flex flex-wrap items-center gap-2 border-t border-border/60 pt-2.5">
          {/* Grade filter chips */}
          <div className="flex flex-wrap items-center gap-1">
            <FilterChip active={gradeFilter === 'all'} onClick={() => setGradeFilter('all')}>
              全部学段
            </FilterChip>
            {GRADES.map((g) => (
              <FilterChip key={g.key} active={gradeFilter === g.key} onClick={() => setGradeFilter(g.key)}>
                {g.name}
              </FilterChip>
            ))}
          </div>
          <div className="mx-1 h-4 w-px bg-border" />
          {/* Subject filter */}
          <select
            className="input !w-auto !py-1 !text-xs"
            value={subjectFilter}
            onChange={(e) => setSubjectFilter(e.target.value)}
          >
            <option value="all">全部科目</option>
            {subjects.map((s) => (
              <option key={s.key} value={s.key}>
                {s.label}
              </option>
            ))}
          </select>
          {/* Sort — pushed to the right */}
          <div className="ml-auto flex items-center gap-2">
            <select
              className="input !w-auto !py-1 !text-xs"
              value={sortKey}
              onChange={(e) => setSortKey(e.target.value)}
            >
              {COURSE_SORT_OPTIONS.map((o) => (
                <option key={o.key} value={o.key}>
                  {o.label}
                </option>
              ))}
            </select>
            <button
              className="btn-ghost btn-sm"
              onClick={() => setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))}
              title={sortDir === 'asc' ? '当前：正序（点击切换为倒序）' : '当前：倒序（点击切换为正序）'}
            >
              {sortDir === 'asc' ? <ArrowUp size={14} /> : <ArrowDown size={14} />}
            </button>
          </div>
        </div>
      </div>

      {filtered.length === 0 ? (
        <EmptyState icon={<Search size={32} />} title="无匹配课程" hint="尝试调整搜索或筛选条件" />
      ) : (
        <div className="flex flex-col gap-2 pb-8">
          {filtered.map((c) => {
            const isOpen = expanded.has(c.id);
            // Effective cover: explicit cover → backend-derived first-episode
            // fallback → styled CSS placeholder (subject gradient + first char).
            // The placeholder is generated in-browser so there's no font/storage
            // cost; it just looks better than a bare emoji. Resolve the subject
            // meta from the reactive subjects list (not the module cache) so the
            // first-paint-before-catalog-loads race doesn't render the raw key +
            // grey fallback here either.
            const meta = subjects.find((x) => x.key === c.subject) ?? subjectMeta(c.subject);
            const cover = c.cover_url || c.cover_fallback_url || '';
            return (
              <div key={c.id} className="overflow-hidden rounded-lg border border-border/60 bg-card">
                {/* Card header — the whole row is the expand affordance. The
                    right-side ⋯ menu lives in a stopPropagation wrapper so its
                    clicks don't bubble up and toggle the card. */}
                <div
                  className="group flex items-center gap-3 px-4 py-3 transition-colors hover:bg-card-2/40"
                  onClick={() => toggleExpand(c.id)}
                >
                  {/* Expand indicator — small chevron at the left, rotates open.
                      Replaces the old huge violet ▶ button. */}
                  <ChevronRight
                    size={16}
                    className={`flex-shrink-0 text-muted transition-transform duration-150 ${isOpen ? 'rotate-90' : ''}`}
                  />

                  {/* Cover thumbnail — h-16 w-16, a touch smaller than the old h-20 */}
                  <div className="h-16 w-16 flex-shrink-0 overflow-hidden rounded-md border border-border bg-card-2">
                    {cover ? (
                      <img src={cover} alt="" className="h-full w-full object-cover" onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')} />
                    ) : (
                      <div
                        className="flex h-full w-full items-center justify-center"
                        style={{ background: `linear-gradient(135deg, ${meta.color}33, ${meta.color}0d)` }}
                      >
                        <SubjectIcon subject={c.subject} size={22} />
                      </div>
                    )}
                  </div>

                  <div className="min-w-0 flex-1">
                    {/* Primary: title */}
                    <h3 className="truncate text-base font-semibold text-txt">{c.title}</h3>
                    {/* Secondary: meta line — subject badge + grade + counts +
                        duration + updated, dot-separated, all muted. Replaces
                        the old 6+ individually-styled spans. */}
                    <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
                      <SubjectBadge subject={c.subject} />
                      <CourseUnlockBadge courseId={c.id} />
                      {c.grade_display && (
                        <>
                          <Sep />
                          <span>{c.grade_display}</span>
                        </>
                      )}
                      <Sep />
                      <span className="tabular-nums">{c.chapter_count ?? 0} 章</span>
                      <Sep />
                      <span className="tabular-nums">{c.episode_count ?? 0} 课时</span>
                      {c.total_duration_seconds ? (
                        <>
                          <Sep />
                          <span className="whitespace-nowrap tabular-nums">{formatDurationShort(c.total_duration_seconds)}</span>
                        </>
                      ) : null}
                      {c.updated_at && (
                        <>
                          <Sep />
                          <span>更新 {relativeTime(c.updated_at)}</span>
                        </>
                      )}
                    </div>
                    {c.tags_list && c.tags_list.length > 0 && (
                      <div className="mt-1.5 flex flex-wrap gap-1">
                        {c.tags_list.map((t) => (
                          <Tag key={t}>{t}</Tag>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* Action menu — wrapper stops propagation so opening the
                      menu (or picking an item) never toggles the card expand. */}
                  <div className="flex flex-shrink-0 items-center" onClick={(e) => e.stopPropagation()}>
                    <DropdownMenu
                      align="right"
                      items={[
                        { label: isOpen ? '折叠课时' : '展开课时', icon: isOpen ? <ChevronUp size={14} /> : <ChevronDown size={14} />, onClick: () => toggleExpand(c.id) },
                        { label: '解锁节奏', icon: <Clock size={14} />, onClick: () => setUnlockForCourse(c) },
                        { label: '编辑课程', icon: <Pencil size={14} />, onClick: () => onEdit(c) },
                        { label: '删除课程', icon: <Trash2 size={14} />, danger: true, onClick: () => onDeleteCourse(c) },
                      ]}
                    />
                  </div>
                </div>

                {/* CourseTree brings its own border-t + bg-card + p-4, so render
                    it directly under the header with no extra wrapper/separator. */}
                {isOpen && <CourseTree course={c} onChanged={onChanged} />}
              </div>
            );
          })}
        </div>
      )}

      {unlockForCourse && (
        <CourseUnlockTemplateModal
          courseId={unlockForCourse.id}
          courseTitle={unlockForCourse.title}
          onClose={() => setUnlockForCourse(null)}
          onSaved={() => qc.invalidateQueries({ queryKey: ['unlock-template'] })}
        />
      )}
    </div>
  );
}

// Sep — a muted dot between meta items, cleaner than separate "·" spans.
function Sep() {
  return <span className="text-border">·</span>;
}

// CourseUnlockBadge shows the effective unlock cadence as a small pill on the
// course card header (e.g. "每周解锁"). Hidden for all_open / no-template
// (the default backward-compat state) so default courses stay visually clean.
// This is a separate component because each course needs its own query hook —
// you can't call useUnlockTemplate inside the map callback.
function CourseUnlockBadge({ courseId }: { courseId: number }) {
  const tplQ = useUnlockTemplate(courseId);
  const t = tplQ.data;
  if (!t || !t.exists || t.strategy === 'all_open') return null;
  return (
    <span
      className="rounded border border-warn/30 bg-warn/5 px-1.5 py-0.5 text-[10px] font-medium text-warn"
      title="该课程设置了视频按需解锁节奏"
    >
      {strategyLabel(t.strategy)}
    </span>
  );
}

function FilterChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
        active ? 'bg-txt text-bg' : 'text-muted hover:bg-card-2 hover:text-txt'
      }`}
    >
      {children}
    </button>
  );
}
