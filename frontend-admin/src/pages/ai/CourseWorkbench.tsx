// CourseWorkbench — 课程级 AI 工作台。"对象即导航":围绕「一门课」把它的所有
// AI 事务聚合到一个页面,而不是散在多个按功能类型切的 tab 里。
//
// 5 个 tab(对象内部切"切面",符合专业后台"顶部 tab ≤5"规则):
//   - 概览:这门课的 AI 状态全景(总结/作业/术语待审/失败任务)
//   - 内容:生成/删除 总结+作业+润色(原 CourseRegenColumn)
//   - Prompt:这门课的 prompt 覆盖 + 即时预览(页内预览,不跳外部页面)
//   - 术语:这门课的术语候选 + 接受后一键润色(接受完就地应用,不跨 tab)
//   - 质量:这门课的错题/考试观测 + 行动入口(错题本带行动出口)
//
// Tab state 在 URL(?tab=...),刷新/分享链接落点不变。courseId 来自路由参数
// /admin/ai/course/:courseId,工作台全程在这个课程上下文里,组件不再需要
// "选课程"下拉(传 courseId prop 让子组件进入"已固定课程"模式)。

import { useSearchParams, useParams, Navigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { useSubjects } from '../../lib/useSubjects';
import { useMemo } from 'react';
import { PageHeader } from '../../components/PageHeader';
import { CourseContentTab } from './course-workbench/CourseContentTab';
import { CourseGlossaryTab } from './course-workbench/CourseGlossaryTab';
import { CourseOverviewTab } from './course-workbench/CourseOverviewTab';
import { CoursePromptTab } from './course-workbench/CoursePromptTab';
import { CourseQualityTab } from './course-workbench/CourseQualityTab';

// 分组标题 + tab。tab.key 用于 URL(?tab=...),label 是顶部 tab 条的显示文案。
// hint 悬浮在 tab 上,告诉用户这个切面是干嘛的(降低"5 个 tab 叫什么"的认知成本)。
const TABS = [
  { key: 'overview', label: '概览', hint: '这门课的 AI 状态全景' },
  { key: 'content', label: '内容', hint: '生成/删除 总结、作业、字幕润色' },
  { key: 'prompt', label: 'Prompt', hint: '这门课的 AI 提示词覆盖 + 即时预览' },
  { key: 'glossary', label: '术语', hint: '字幕润色挖出的术语候选,审核后可一键应用' },
  { key: 'quality', label: '质量', hint: '错题/考试观测,发现题面或难点问题' },
] as const;

type TabKey = (typeof TABS)[number]['key'];
const DEFAULT_TAB: TabKey = 'overview';

function isTabKey(s: string | null): s is TabKey {
  return !!s && (TABS as readonly { key: string }[]).some((t) => t.key === s);
}

export function CourseWorkbench() {
  const { courseId: courseIdStr } = useParams<{ courseId: string }>();
  const [params, setParams] = useSearchParams();
  const courseId = Number(courseIdStr);
  const validCourseId = Number.isFinite(courseId) && courseId > 0 ? courseId : null;

  const rawTab = params.get('tab');
  const tab: TabKey = isTabKey(rawTab) ? rawTab : DEFAULT_TAB;
  const setTab = (t: string) => {
    const next = new URLSearchParams(params);
    next.set('tab', t);
    setParams(next, { replace: true });
  };

  // 课程基本信息——给 PageHeader 显示课程名 + 学科。课程工作台的"我在哪门课"
  // 上下文要一眼可见,避免 admin 在多个工作台间迷路。
  const courseQ = useQuery({
    queryKey: ['courses'],
    queryFn: api.listCourses,
  });
  const subjectsQ = useSubjects();
  const course = useMemo(
    () => (courseQ.data ?? []).find((c) => c.id === validCourseId) ?? null,
    [courseQ.data, validCourseId],
  );
  const subjectLabel = useMemo(() => {
    if (!course?.subject) return '';
    const s = (subjectsQ.data ?? []).find((x) => x.key === course.subject);
    return s?.label ?? course.subject;
  }, [course?.subject, subjectsQ.data]);

  // 路由参数不合法(非数字 courseId)→ 回到课程工作台列表,不展示空壳。
  if (validCourseId == null) {
    return <Navigate to="/admin/ai/courses" replace />;
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={course ? course.title : `课程 #${validCourseId}`}
        breadcrumb={[
          { label: 'AI 运营' },
          { label: '课程工作台', to: '/admin/ai/courses' },
        ]}
        description={
          subjectLabel
            ? `学科:${subjectLabel} · 围绕这门课集中管理 AI 内容、Prompt、术语与质量`
            : '围绕这门课集中管理 AI 内容、Prompt、术语与质量'
        }
      />

      {/* 顶部 tab 条:对象内部切"切面"。≤5 项,短标签,符合专业后台规则。 */}
      <div className="flex flex-wrap gap-1.5 border-b border-border">
        {TABS.map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            title={t.hint}
            className={`rounded-t-md px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key ? 'border-b-2 border-primary text-primary' : 'text-muted hover:text-txt'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div>
        {/* courseId 作为 prop 传入,子组件进入"课程已固定"模式,不再渲染选课程下拉。 */}
        {tab === 'overview' && <CourseOverviewTab courseId={validCourseId} />}
        {tab === 'content' && <CourseContentTab courseId={validCourseId} />}
        {tab === 'prompt' && <CoursePromptTab courseId={validCourseId} />}
        {tab === 'glossary' && <CourseGlossaryTab courseId={validCourseId} />}
        {tab === 'quality' && <CourseQualityTab courseId={validCourseId} />}
      </div>
    </div>
  );
}
