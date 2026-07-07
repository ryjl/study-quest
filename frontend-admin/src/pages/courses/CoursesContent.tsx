import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { GRADES, subjectMeta, type Course } from '../../lib/types';
import { useSubjects } from '../../lib/useSubjects';
import { EmptyState, LoadingState, SubjectBadge, Tag } from '../../components/ui';
import { formatDurationShort, relativeTime } from '../../lib/format';
import { useToast, useConfirm } from '../../lib/toast';
import { CourseTree } from './CourseTree';

export function CoursesContent({ onEdit, onChanged }: { onEdit: (c: Course) => void; onChanged: () => void }) {
  const [search, setSearch] = useState('');
  const [gradeFilter, setGradeFilter] = useState('all');
  const [subjectFilter, setSubjectFilter] = useState('all');
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const qc = useQueryClient();
  const toast = useToast();
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];
  const confirm = useConfirm();

  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const courses = coursesQ.data ?? [];

  const filtered = useMemo(() => {
    return courses.filter((c) => {
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
  }, [courses, gradeFilter, subjectFilter, search]);

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
        <div className="ml-auto text-sm text-muted">
          {filtered.length} / {courses.length} 门课程
        </div>
      </div>

      {filtered.length === 0 ? (
        <EmptyState icon="🔍" title="无匹配课程" hint="尝试调整搜索或筛选条件" />
      ) : (
        <div className="flex flex-col gap-5 pb-8">
          {filtered.map((c) => {
            const isOpen = expanded.has(c.id);
            return (
              <div key={c.id} className="card !p-0 overflow-hidden">
                {/* Card header */}
                <div className="flex items-center gap-4 p-5">
                  {/* Cover thumbnail */}
                  <div className="h-16 w-16 flex-shrink-0 overflow-hidden rounded-xl border border-border bg-card-2">
                    {c.cover_url ? (
                      <img src={c.cover_url} alt="" className="h-full w-full object-cover" onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')} />
                    ) : (
                      <div className="flex h-full w-full items-center justify-center text-2xl opacity-40">{subjectMeta(c.subject).emoji}</div>
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
