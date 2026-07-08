import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { GRADES, subjectMeta, type Course } from '../../lib/types';
import { useSubjects } from '../../lib/useSubjects';
import { EmptyState, LoadingState, SubjectBadge, Tag } from '../../components/ui';
import { formatDurationShort, relativeTime } from '../../lib/format';
import { useToast, useConfirm } from '../../lib/toast';
import { sortBy, timeValue, type SortDir, type SortOption } from '../../lib/sort';
import { CourseTree } from './CourseTree';

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
        const hay = `${c.title} ${c.subject} ${c.tags}`.toLowerCase();
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
    return <EmptyState icon="📚" title="课程库为空" hint="点击右上角「+ 新增课程库」创建您的第一个课程。" />;
  }

  return (
    <div>
      {/* Filter toolbar */}
      <div className="mb-5 flex flex-wrap items-center gap-3 rounded-xl border border-border bg-card p-3">
        <input
          className="input max-w-xs"
          placeholder="🔍 搜索课程名 / 标签..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <select className="input max-w-[180px]" value={subjectFilter} onChange={(e) => setSubjectFilter(e.target.value)}>
          <option value="all">全部科目</option>
          {subjects.map((s) => (
            <option key={s.key} value={s.key}>
              {s.emoji} {s.label}
            </option>
          ))}
        </select>
        <div className="flex flex-wrap gap-1.5">
          <FilterChip active={gradeFilter === 'all'} onClick={() => setGradeFilter('all')}>
            全部学段
          </FilterChip>
          {GRADES.map((g) => (
            <FilterChip key={g.key} active={gradeFilter === g.key} onClick={() => setGradeFilter(g.key)}>
              {g.name}
            </FilterChip>
          ))}
        </div>
        <div className="ml-auto flex items-center gap-2">
          <span className="text-xs text-muted">排序</span>
          <select
            className="input !py-1 !text-xs max-w-[140px]"
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
            {sortDir === 'asc' ? '↑ 正序' : '↓ 倒序'}
          </button>
        </div>
        <div className="text-sm text-muted">
          {filtered.length} / {courses.length} 门课程
        </div>
      </div>

      {filtered.length === 0 ? (
        <EmptyState icon="🔍" title="无匹配课程" hint="尝试调整搜索或筛选条件" />
      ) : (
        <div className="flex flex-col gap-5 pb-8">
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
              <div key={c.id} className="card !p-0 overflow-hidden">
                {/* Card header */}
                <div className="flex items-center gap-4 p-5">
                  {/* Cover thumbnail */}
                  <div className="h-16 w-16 flex-shrink-0 overflow-hidden rounded-xl border border-border bg-card-2">
                    {cover ? (
                      <img src={cover} alt="" className="h-full w-full object-cover" onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')} />
                    ) : (
                      <div
                        className="flex h-full w-full flex-col items-center justify-center"
                        style={{ background: `linear-gradient(135deg, ${meta.color}40, ${meta.color}10)` }}
                      >
                        <span className="text-xl leading-none opacity-80">{meta.emoji}</span>
                        <span className="mt-0.5 max-w-full truncate text-[10px] font-bold" style={{ color: meta.color }}>
                          {(c.title || '').slice(0, 1)}
                        </span>
                      </div>
                    )}
                  </div>

                  <div className="min-w-0 flex-1">
                    <h3 className="truncate text-lg font-bold text-txt">{c.title}</h3>
                    <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
                      <SubjectBadge subject={c.subject} />
                      <span className="text-muted">{c.grade_display}</span>
                      <span className="text-muted">·</span>
                      <span className="text-muted">{c.chapter_count ?? 0} 章</span>
                      <span className="text-muted">·</span>
                      <span className="text-muted">{c.episode_count ?? 0} 课时</span>
                      {c.total_duration_seconds ? (
                        <>
                          <span className="text-muted">·</span>
                          <span className="text-warn">⏱ {formatDurationShort(c.total_duration_seconds)}</span>
                        </>
                      ) : null}
                      {c.updated_at && (
                        <>
                          <span className="text-muted">·</span>
                          <span className="text-muted">更新 {relativeTime(c.updated_at)}</span>
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

                  <div className="flex flex-shrink-0 gap-2">
                    <button className="btn-secondary btn-sm" onClick={() => toggleExpand(c.id)} title={isOpen ? '折叠' : '展开'}>
                      {isOpen ? '▲ 收起' : '▼ 展开'}
                    </button>
                    <button className="btn-secondary btn-sm" onClick={() => onEdit(c)}>
                      ✏ 编辑
                    </button>
                    <button className="btn-danger btn-sm" onClick={() => onDeleteCourse(c)}>
                      删除
                    </button>
                  </div>
                </div>

                {isOpen && <CourseTree course={c} onChanged={onChanged} />}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function FilterChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-lg px-3 py-1 text-xs font-medium transition ${
        active ? 'bg-primary text-white shadow-primary-glow' : 'bg-card-2 text-txt hover:bg-muted'
      }`}
    >
      {children}
    </button>
  );
}
