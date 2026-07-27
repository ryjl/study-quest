// CoursePromptTab — 课程工作台「Prompt」tab。修复旧版断裂 5:"编辑完课程 prompt 覆盖
// 要跳到课程页才能预览"。这里编辑 + 即时预览同页。
//
// 课程已由路由固定(无需选课程下拉)。复用 PromptConfigTab CoursePromptSection 的
// 编辑逻辑(5 字段 ai_config + 两个 AI 开关 + 套用学科模板),但把"到课程页预览"按钮
// 换成页内即时预览(打开 PreviewPromptModal,不再跳走)。
//
// CRITICAL(同 PromptConfigTab):后端 UpdateCourse 是 PUT 全量替换,必须发完整 course
// body(title/subject 等原值回传),否则 400 或误清字段。
import { useEffect, useRef, useState } from 'react';
import { useQuery, useMutation } from '@tanstack/react-query';
import { Eye } from 'lucide-react';
import { api } from '../../../lib/api';
import { useToast } from '../../../lib/toast';
import { useSubjects } from '../../../lib/useSubjects';
import { AIHintFields, emptyAiHintValue, type AiHintFieldsValue } from '../../ai-console/AIHintFields';
import { PreviewPromptModal } from '../../../components/ai/PreviewPromptModal';

export function CoursePromptTab({ courseId }: { courseId: number }) {
  const toast = useToast();
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const subjectsQ = useSubjects();
  const course = (coursesQ.data ?? []).find((c) => c.id === courseId);
  const courseSubject = subjectsQ.data?.find((s) => s.key === course?.subject);
  const courseSubjectLabel = courseSubject?.label ?? course?.subject ?? '';

  const [cfg, setCfg] = useState<AiHintFieldsValue>(emptyAiHintValue());
  const [aiSummaryEnabled, setAiSummaryEnabled] = useState(false);
  const [aiQuizEnabled, setAiQuizEnabled] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);

  // 课程数据到达后回填表单——但只回填一次(用 ref 标记),避免 catalog refetch 时
  // 覆盖 admin 正在编辑的草稿。原 PromptConfigTab 依赖 [courseId](稳定 id)达成这点;
  // 这里 course 数据是异步到的,所以用 ref:首次拿到 course 时回填,之后不再覆盖。
  // 保存成功后的 refetch 不会重置表单(admin 看到自己刚保存的值仍在,符合预期)。
  const initialized = useRef(false);
  useEffect(() => {
    if (initialized.current || !course) return;
    initialized.current = true;
    const ac = course.ai_config;
    setCfg({
      whisper_hint: ac?.whisper_hint ?? course.whisper_hint ?? '',
      summary_hint: ac?.summary_hint ?? '',
      quiz_hint: ac?.quiz_hint ?? course.quiz_hint ?? '',
      advice_hint: ac?.advice_hint ?? '',
      term_dict: ac?.term_dict ?? '',
    });
    setAiSummaryEnabled(course.ai_summary_enabled ?? false);
    setAiQuizEnabled(course.ai_quiz_enabled ?? false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [course]);

  const applyTemplate = () => {
    // 套用学科默认(学科 AI 配置现在在 Subjects 页编辑)。学科未配则提示去配。
    if (!courseSubject) {
      toast.error('该课程对应的学科未配置默认 Prompt');
      return;
    }
    const sc = courseSubject.ai_config;
    if (!sc || (!sc.whisper_hint?.trim() && !sc.summary_hint?.trim() && !sc.quiz_hint?.trim() && !sc.advice_hint?.trim() && !sc.term_dict?.trim())) {
      toast.error(`学科「${courseSubjectLabel}」未配置默认 Prompt,请先到「分类与标签」编辑该学科`);
      return;
    }
    setCfg({
      whisper_hint: sc.whisper_hint ?? '',
      summary_hint: sc.summary_hint ?? '',
      quiz_hint: sc.quiz_hint ?? '',
      advice_hint: sc.advice_hint ?? '',
      term_dict: sc.term_dict ?? '',
    });
    toast.success(`已套用「${courseSubjectLabel}」学科默认,可继续微调`);
  };

  const saveMut = useMutation({
    mutationFn: async () => {
      if (!course) throw new Error('课程不存在');
      // 必须发完整 course body:后端 UpdateCourse 用 binding:"required" 强制 title+subject。
      // 把课程本体字段原值回传,只覆盖 ai_config + 两个 AI 开关(同 CourseModal/PromptConfigTab)。
      const gradeValue = course.grades ?? course.grade ?? '';
      const body = {
        title: course.title,
        grades: gradeValue,
        grade: gradeValue,
        subject: course.subject,
        content_type: course.content_type,
        cover_url: course.cover_url,
        tag_ids: course.tag_ids ?? [],
        ai_config: {
          whisper_hint: cfg.whisper_hint.trim(),
          summary_hint: cfg.summary_hint.trim(),
          quiz_hint: cfg.quiz_hint.trim(),
          advice_hint: cfg.advice_hint.trim(),
          term_dict: cfg.term_dict.trim(),
        },
        ai_summary_enabled: aiSummaryEnabled,
        ai_quiz_enabled: aiQuizEnabled,
      };
      return api.updateCourse(courseId, body);
    },
    onSuccess: () => {
      toast.success('课程 Prompt 已保存');
      coursesQ.refetch();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  if (coursesQ.isLoading) {
    return <div className="rounded-lg border border-border bg-card p-6 text-sm text-muted">加载中…</div>;
  }
  if (!course) {
    return <div className="rounded-lg border border-border bg-card p-6 text-sm text-muted">课程不存在</div>;
  }

  return (
    <section className="space-y-3 rounded-lg border border-border bg-card p-4">
      <header className="space-y-0.5">
        <h2 className="text-base font-semibold">课程 Prompt 覆盖</h2>
        <p className="text-xs text-muted">
          课程级字段覆盖学科默认(除 term_dict 为合并)。下方两个开关控制该课程是否触发 AI 总结/出题。
          {courseSubjectLabel && <>当前学科:{courseSubjectLabel}。</>}
        </p>
      </header>

      <AIHintFields
        value={cfg}
        onChange={setCfg}
        showApplyTemplateButton
        onApplyTemplate={applyTemplate}
        applyTemplateLabel={courseSubjectLabel ? `套用「${courseSubjectLabel}」模板` : '套用学科模板'}
      />

      <div className="space-y-2 rounded-xl border border-border bg-card-2 p-3">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" className="h-4 w-4 rounded border-border accent-primary" checked={aiSummaryEnabled} onChange={(e) => setAiSummaryEnabled(e.target.checked)} />
          <span>启用 AI 总结</span>
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" className="h-4 w-4 rounded border-border accent-primary" checked={aiQuizEnabled} onChange={(e) => setAiQuizEnabled(e.target.checked)} />
          <span>启用 AI 出题</span>
        </label>
        <p className="text-[11px] text-muted">关闭后,该课程的课时将跳过对应的 AI 后处理(即使全局已配置)。</p>
      </div>

      <div className="flex items-center justify-between gap-2">
        {/* 即时预览:页内打开 PreviewPromptModal,不再跳外部页面(修复旧版断裂)。
            让"改完立刻看效果"成为工作台的标配——调优最需要的就是这个即时反馈环。 */}
        <button
          type="button"
          className="btn-ghost btn-sm inline-flex items-center gap-1.5"
          onClick={() => setPreviewOpen(true)}
          title="预览本课程最终拼出的完整 prompt(不调 LLM)"
        >
          <Eye size={14} /> 即时预览 Prompt
        </button>
        <button className="btn-primary" onClick={() => saveMut.mutate()} disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中…' : '保存课程覆盖'}
        </button>
      </div>

      {previewOpen && (
        <PreviewPromptModal courseId={courseId} courseTitle={course.title} onClose={() => setPreviewOpen(false)} />
      )}
    </section>
  );
}
