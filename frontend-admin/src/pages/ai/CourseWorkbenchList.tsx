// CourseWorkbenchList — 课程工作台入口页(选哪门课进入它的 AI 工作台)。
// 复用课程列表,每行加"进入 AI 工作台"入口。不做复杂筛选(那是课程库管理页的职责),
// 这里就是一个简洁的"选课进入工作台"的中转页。
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Bot, ChevronRight } from 'lucide-react';
import { api } from '../../lib/api';
import { useSubjects } from '../../lib/useSubjects';
import { useMemo } from 'react';
import { PageHeader } from '../../components/PageHeader';
import { Spinner } from '../../components/ui';

export function CourseWorkbenchList() {
  const navigate = useNavigate();
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const subjectsQ = useSubjects();
  const subjectLabel = useMemo(() => {
    const m = new Map<string, string>();
    for (const s of subjectsQ.data ?? []) m.set(s.key, s.label);
    return m;
  }, [subjectsQ.data]);

  if (coursesQ.isLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <Spinner size={24} />
      </div>
    );
  }

  const courses = coursesQ.data ?? [];

  return (
    <div>
      <PageHeader
        title="课程工作台"
        breadcrumb={[{ label: 'AI 运营' }]}
        description="选择一门课程,进入它的 AI 工作台 —— 围绕这门课集中管理内容产出、Prompt、术语与质量。"
      />
      {courses.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border bg-card px-4 py-12 text-center text-sm text-muted">
          还没有课程。请先到「课程库管理」创建课程。
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {courses.map((c) => (
            <button
              key={c.id}
              onClick={() => navigate(`/admin/ai/course/${c.id}`)}
              className="group flex items-center gap-3 rounded-lg border border-border bg-card p-3 text-left transition-colors hover:border-primary/40 hover:bg-card-2/50"
            >
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
                <Bot size={16} />
              </span>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium text-txt">{c.title}</div>
                <div className="text-[11px] text-muted">
                  {c.subject ? subjectLabel.get(c.subject) ?? c.subject : ''}
                  {c.episode_count ? ` · ${c.episode_count} 课时` : ''}
                </div>
              </div>
              <ChevronRight size={16} className="shrink-0 text-muted opacity-0 transition-opacity group-hover:opacity-100" />
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
