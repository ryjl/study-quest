// StudentContentTab — 学生工作台「题库与建议」tab。顶部课程过滤器,选中后题库区和
// 建议区联动到该课程上下文,消除"题库按 episode、建议按 scope,两套维度割裂"的困惑。
//
// 组成:
//   - 顶部:课程选择器(全部 + 该生有数据的课程)。
//   - 题库区:复用 AIUserView(courseFilter 过滤),含题库列表 + 详情 modal。
//   - 建议区:StudentAdviceSection(courseFilter 过滤 + 显示课程名)。
//
// 关键:advice 的 episode scope 的 scope_id 是 episode_id,要判断该 episode 属于哪门课
// 才能按课程过滤。这里拉每门课的 episodes 建 episode→course 映射,传给 advice section。
import { useMemo, useState } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { api } from '../../../lib/api';
import { AIUserView } from '../../AIUserView';
import { StudentAdviceSection } from './StudentAdviceSection';

export function StudentContentTab({ userId }: { userId: number }) {
  // 课程过滤器:null=全部,数字=某课程 id。
  const [courseFilter, setCourseFilter] = useState<number | null>(null);
  const quizzesQ = useQuery({
    queryKey: ['ai-user-quizzes', userId],
    queryFn: () => api.listUserQuizzes(userId),
  });
  const adviceQ = useQuery({
    queryKey: ['ai-user-advice', userId],
    queryFn: () => api.listUserAdvice(userId),
  });
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const courses = coursesQ.data ?? [];

  const courseTitleFor = useMemo(() => {
    const m = new Map<number, string>();
    for (const c of courses) m.set(c.id, c.title);
    return (cid: number) => m.get(cid) ?? `课程 ${cid}`;
  }, [courses]);

  // episode_id → course_id 映射。advice 的 episode scope 的 scope_id 是 episode_id,
  // 要按课程过滤 advice 就必须知道每个 episode 属于哪门课。批量拉每门课的 episodes 建映射。
  // 课程数小(family-scale),每门课一次请求可接受。
  const episodesQueries = useQueries({
    queries: courses.map((c) => ({
      queryKey: ['course-episodes', c.id],
      queryFn: () => api.listEpisodes(c.id),
      staleTime: 60_000,
    })),
  });
  const episodeToCourse = useMemo(() => {
    const m = new Map<number, number>();
    courses.forEach((c, i) => {
      for (const ep of episodesQueries[i].data ?? []) m.set(ep.id, c.id);
    });
    return m;
  }, [courses, episodesQueries]);

  // episode_id → title 映射。episode scope 的建议 scope_id 是 episode_id,卡片要显示
  // "哪节课的建议"必须能查到 episode 标题,否则一堆"课时建议"分不清是哪节。
  // 数据源和 episodeToCourse 同(已拉每门课的 episodes),不额外发请求。
  const episodeTitleFor = useMemo(() => {
    const m = new Map<number, string>();
    courses.forEach((_c, i) => {
      for (const ep of episodesQueries[i].data ?? []) m.set(ep.id, ep.title);
    });
    return (eid: number) => m.get(eid);
  }, [courses, episodesQueries]);

  // 该生有数据的课程(题库或建议涉及到的)——给过滤器选项,避免列一堆空课程。
  // advice 的 episode scope_id 是 episode_id,用 episodeToCourse 映射成 course_id。
  const courseOptions = useMemo(() => {
    const courseIds = new Set<number>();
    for (const q of quizzesQ.data ?? []) courseIds.add(q.course_id);
    for (const a of adviceQ.data ?? []) {
      if (a.scope === 'course') courseIds.add(a.scope_id);
      else if (a.scope === 'episode') {
        const cid = episodeToCourse.get(a.scope_id);
        if (cid != null) courseIds.add(cid);
      }
    }
    return courses.filter((c) => courseIds.has(c.id));
  }, [quizzesQ.data, adviceQ.data, courses, episodeToCourse]);

  return (
    <div className="space-y-6">
      {/* 课程过滤器:让 admin 聚焦一门课看它的题库+建议,消除两区维度割裂。 */}
      {courseOptions.length > 0 && (
        <div className="flex items-center gap-2 rounded-lg border border-border bg-card p-3">
          <label className="text-xs text-muted">按课程筛选:</label>
          <select
            className="input !py-1 !text-xs max-w-xs"
            value={courseFilter ?? ''}
            onChange={(e) => setCourseFilter(e.target.value ? Number(e.target.value) : null)}
          >
            <option value="">全部课程</option>
            {courseOptions.map((c) => (
              <option key={c.id} value={c.id}>
                {c.title}
              </option>
            ))}
          </select>
        </div>
      )}

      <AIUserView key={userId} embedded userId={userId} courseFilter={courseFilter ?? undefined} />
      <StudentAdviceSection userId={userId} courseFilter={courseFilter} courseTitleFor={courseTitleFor} episodeCourseMap={episodeToCourse} episodeTitleFor={episodeTitleFor} />
    </div>
  );
}
