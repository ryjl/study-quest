import { useEffect, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { ExternalLink } from 'lucide-react';
import { api } from '../../lib/api';
import { useToast } from '../../lib/toast';
import { useSubjects, useInvalidateSubjects } from '../../lib/useSubjects';
import type { Course, SubjectMeta } from '../../lib/types';
import { AIHintFields, emptyAiHintValue, type AiHintFieldsValue } from './AIHintFields';
import { HomeworkPromptSection } from './HomeworkPromptSection';

// PromptConfigTab — the "Prompt 配置" tab on the AI Console. Two stacked
// sections:
//   1. 学科默认 (subject ai_config): pick a subject, edit its 5 fields, save.
//   2. 课程覆盖 (course ai_config): pick a course, edit its 5 fields + two
//      enable switches, save. Course fields override subject defaults at
//      resolve-time; term_dict is special (concat instead of override).
//
// This is the SAME data CourseModal/SubjectModal edit — centralizing the
// prompt-only view here lets the admin tune prompts at a glance without
// opening the full create/edit modal. CRITICAL: 后端 UpdateCourse/UpdateSubject
// 都是 PUT 全量替换(Gin binding:"required" 强制 title/subject/key/label 非空),
// 所以本 tab 的 save body 必须发完整对象(把课程/学科本体字段原值回传,只覆盖 ai_config
// 相关字段)。否则 save 会 400,或更糟 —— 把 title/subject 等字段误清。
//
// Subject save shape (mirrors Subjects.tsx SubjectModal):
//   { key, label, color, sort_order, ai_config: { 5 fields } }
// Course save shape (mirrors CourseModal):
//   { title, grades, grade, subject, content_type, cover_url, tag_ids,
//     ai_config: { 5 fields }, ai_summary_enabled, ai_quiz_enabled }

export function PromptConfigTab() {
  return (
    <div className="space-y-6">
      <SubjectPromptSection />
      <CoursePromptSection />
      <HomeworkPromptSection />
    </div>
  );
}

// ---------------- Subject default ----------------

function SubjectPromptSection() {
  const subjectsQ = useSubjects();
  const invalidate = useInvalidateSubjects();
  const toast = useToast();
  const subjects = subjectsQ.data ?? [];

  // 读 URL ?subject=<id> 参数:SubjectModal 的"配置 →"跳转带这个过来,落地预选。
  const [params] = useSearchParams();
  const initialSubjectId = (() => {
    const raw = params.get('subject');
    if (!raw) return null;
    const n = Number(raw);
    return Number.isFinite(n) && n > 0 ? n : null;
  })();

  const [subjectId, setSubjectId] = useState<number | null>(initialSubjectId);
  const [cfg, setCfg] = useState<AiHintFieldsValue>(emptyAiHintValue());

  const selectedSubject: SubjectMeta | undefined = subjects.find((s) => s.id === subjectId);

  // Resync the form whenever the picked subject changes. We depend on
  // subjectId only (not on `subjects`) so the catalog arriving late doesn't
  // clobber a value the admin just edited — same defensive pattern as
  // CourseModal's open-effect.
  useEffect(() => {
    if (subjectId == null) {
      setCfg(emptyAiHintValue());
      return;
    }
    const s = subjects.find((x) => x.id === subjectId);
    const c = s?.ai_config;
    setCfg({
      whisper_hint: c?.whisper_hint ?? '',
      summary_hint: c?.summary_hint ?? '',
      quiz_hint: c?.quiz_hint ?? '',
      advice_hint: c?.advice_hint ?? '',
      term_dict: c?.term_dict ?? '',
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [subjectId]);

  const saveMut = useMutation({
    mutationFn: async () => {
      if (subjectId == null) throw new Error('请先选择一个学科');
      const s = subjects.find((x) => x.id === subjectId);
      if (!s) throw new Error('学科不存在');
      // Mirror Subjects.tsx SubjectModal: spread the existing subject fields
      // (key/label/color/sort_order so the PUT doesn't blank them), override
      // ai_config with the 5 trimmed fields. ai_config is sent as a whole —
      // backend handler sees non-nil and overwrites.
      const body = {
        key: s.key,
        label: s.label,
        color: s.color,
        sort_order: s.sort_order ?? 0,
        ai_config: {
          whisper_hint: cfg.whisper_hint.trim(),
          summary_hint: cfg.summary_hint.trim(),
          quiz_hint: cfg.quiz_hint.trim(),
          advice_hint: cfg.advice_hint.trim(),
          term_dict: cfg.term_dict.trim(),
        },
      };
      return api.updateSubject(subjectId, body);
    },
    onSuccess: () => {
      toast.success('学科默认 Prompt 已保存');
      invalidate();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  if (subjectsQ.isLoading) return <SectionShell title="学科默认 Prompt" loading />;
  if (subjectsQ.error)
    return (
      <SectionShell title="学科默认 Prompt" error={(subjectsQ.error as Error).message} onRetry={() => subjectsQ.refetch()} />
    );

  return (
    <section className="space-y-3 rounded-lg border border-border bg-card p-4">
      <header className="space-y-0.5">
        <h2 className="text-base font-semibold">学科默认 Prompt</h2>
        <p className="text-xs text-muted">该学科下未单独覆盖的课程会回退到这里的提示。term_dict 会"追加"到课程级后面（合并而非覆盖）。</p>
      </header>

      <div>
        <label className="mb-1 block text-xs text-muted">选择学科</label>
        <select
          className="input max-w-md"
          value={subjectId ?? ''}
          onChange={(e) => setSubjectId(e.target.value ? Number(e.target.value) : null)}
        >
          <option value="">— 请选择 —</option>
          {subjects.map((s) => (
            <option key={s.id} value={s.id}>
              {s.label}（{s.key}）{s.is_system ? ' · 系统' : ''}
            </option>
          ))}
        </select>
      </div>

      {selectedSubject ? (
        <>
          <AIHintFields value={cfg} onChange={setCfg} />
          <div className="flex justify-end">
            <button className="btn-primary" onClick={() => saveMut.mutate()} disabled={saveMut.isPending}>
              {saveMut.isPending ? '保存中…' : '保存学科默认'}
            </button>
          </div>
        </>
      ) : (
        <div className="rounded-md border border-dashed border-border bg-card-2 px-4 py-8 text-center text-sm text-muted">
          选择一个学科以编辑其默认 Prompt。
        </div>
      )}
    </section>
  );
}

// ---------------- Course override ----------------

function CoursePromptSection() {
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const subjectsQ = useSubjects();
  const toast = useToast();
  const navigate = useNavigate();
  // 读 URL ?course=<id> 参数:CourseModal 的"配置 →"跳转链接带这个参数过来
  // (/admin/ai-console?tab=prompt&course=123),让 admin 落地时该课程已预选,
  // 不需要再在下拉里翻一遍。useState 惰性初始化只读一次(URL 参数变化不重选是
  // 预期 —— admin 在本 tab 内切别的课程后,不应被 URL 反向覆盖)。
  const [params] = useSearchParams();
  const initialCourseId = (() => {
    const raw = params.get('course');
    if (!raw) return null;
    const n = Number(raw);
    return Number.isFinite(n) && n > 0 ? n : null;
  })();
  const courses = coursesQ.data ?? [];
  const subjects = subjectsQ.data ?? [];

  const [courseId, setCourseId] = useState<number | null>(initialCourseId);
  const [cfg, setCfg] = useState<AiHintFieldsValue>(emptyAiHintValue());
  const [aiSummaryEnabled, setAiSummaryEnabled] = useState(false);
  const [aiQuizEnabled, setAiQuizEnabled] = useState(false);

  const selectedCourse: Course | undefined = courses.find((c) => c.id === courseId);
  // The subject whose ai_config the "套用模板" button copies. Course.subject
  // is a key (not an id), so we resolve it through the catalog.
  const courseSubject = subjects.find((s) => s.key === selectedCourse?.subject);
  const courseSubjectLabel = courseSubject?.label ?? selectedCourse?.subject ?? '';

  // Resync on course change. Read ai_config first (5-field blob), then fall
  // back to the legacy top-level whisper_hint/quiz_hint/ai_hint — same
  // migration path as CourseModal so editing here neither drops legacy data
  // nor double-writes the old fields.
  useEffect(() => {
    if (courseId == null) {
      setCfg(emptyAiHintValue());
      setAiSummaryEnabled(false);
      setAiQuizEnabled(false);
      return;
    }
    const c = courses.find((x) => x.id === courseId);
    const ac = c?.ai_config;
    setCfg({
      whisper_hint: ac?.whisper_hint ?? c?.whisper_hint ?? (c?.ai_hint ?? ''),
      summary_hint: ac?.summary_hint ?? '',
      quiz_hint: ac?.quiz_hint ?? c?.quiz_hint ?? '',
      advice_hint: ac?.advice_hint ?? '',
      term_dict: ac?.term_dict ?? '',
    });
    // AI switches default OFF when unset — AI is an opt-in add-on layer per
    // course. Matches the backend gorm:"default:false".
    setAiSummaryEnabled(c?.ai_summary_enabled ?? false);
    setAiQuizEnabled(c?.ai_quiz_enabled ?? false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [courseId]);

  const applyTemplate = () => {
    // 把选中课程的学科 ai_config 复制到当前编辑的 5 字段。模板源 100% 来自 DB
    // Subject.AIConfig(系统学科如 math/english/xiangqi 由 SeedDefaultSubjects seed,
    // 自定义学科由 admin 配)。如果学科未配,提示 admin 先去上方"学科默认"段配置。
    // (前端曾经的 aiHintTemplates.ts fallback 在 2026-07-19 集中化轮次删除。)
    if (!courseSubject) {
      toast.error('该课程对应的学科未配置默认 Prompt，请先到「学科默认」段配置');
      return;
    }
    const sc = courseSubject.ai_config;
    if (
      !sc ||
      (!sc.whisper_hint?.trim() &&
        !sc.summary_hint?.trim() &&
        !sc.quiz_hint?.trim() &&
        !sc.advice_hint?.trim() &&
        !sc.term_dict?.trim())
    ) {
      toast.error(`学科「${courseSubjectLabel}」未配置默认 Prompt，请先在上方编辑并保存`);
      return;
    }
    setCfg({
      whisper_hint: sc.whisper_hint ?? '',
      summary_hint: sc.summary_hint ?? '',
      quiz_hint: sc.quiz_hint ?? '',
      advice_hint: sc.advice_hint ?? '',
      term_dict: sc.term_dict ?? '',
    });
    toast.success(`已套用「${courseSubjectLabel}」学科默认，可继续微调`);
  };

  const saveMut = useMutation({
    mutationFn: async () => {
      if (courseId == null) throw new Error('请先选择一个课程');
      const c = courses.find((x) => x.id === courseId);
      if (!c) throw new Error('课程不存在');
      // 必须发完整 course body:后端 UpdateCourse handler 用 `binding:"required"`
      // 强制要求 title + subject,只发 ai_config 会被 Gin 拒成 400。所以这里和
      // CourseModal 一样,把课程本体字段(title/grade/subject/cover/tags/content_type)
      // 原值回传,只覆盖 ai_config + 两个 AI 开关。
      //
      // 注意:这意味着如果两个 admin 同时编辑 —— 一个在课程 modal 改 title、一个在
      // 这里改 prompt —— 后保存的会覆盖前者的 title。这是 PUT 全量替换语义的固有权衡,
      // CourseModal 也一样。当前规模(family-scale 单 admin)可接受;真要细粒度并发
      // 控制需要后端改成 PATCH 风格的 merge update,超出本轮范围。
      const gradeValue = c.grades ?? c.grade ?? '';
      const body = {
        title: c.title,
        grades: gradeValue,
        grade: gradeValue,
        subject: c.subject,
        content_type: c.content_type,
        cover_url: c.cover_url,
        tag_ids: c.tag_ids ?? [],
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
      // No local setQueryData: refetch picks up the server's canonical view.
      coursesQ.refetch();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  if (coursesQ.isLoading) return <SectionShell title="课程覆盖 Prompt" loading />;
  if (coursesQ.error)
    return (
      <SectionShell title="课程覆盖 Prompt" error={(coursesQ.error as Error).message} onRetry={() => coursesQ.refetch()} />
    );

  return (
    <section className="space-y-3 rounded-lg border border-border bg-card p-4">
      <header className="space-y-0.5">
        <h2 className="text-base font-semibold">课程覆盖 Prompt</h2>
        <p className="text-xs text-muted">课程级字段会覆盖学科默认（除 term_dict 为合并）。下方两个开关控制该课程是否触发 AI 总结/出题。</p>
      </header>

      <div>
        <label className="mb-1 block text-xs text-muted">选择课程</label>
        <select
          className="input max-w-md"
          value={courseId ?? ''}
          onChange={(e) => setCourseId(e.target.value ? Number(e.target.value) : null)}
        >
          <option value="">— 请选择 —</option>
          {courses.map((c) => (
            <option key={c.id} value={c.id}>
              {c.title}
              {c.subject ? `（${c.subject}）` : ''}
            </option>
          ))}
        </select>
      </div>

      {selectedCourse ? (
        <>
          <AIHintFields
            value={cfg}
            onChange={setCfg}
            showApplyTemplateButton
            onApplyTemplate={applyTemplate}
            applyTemplateLabel={courseSubjectLabel ? `套用「${courseSubjectLabel}」模板` : '套用学科模板'}
          />

          <div className="space-y-2 rounded-xl border border-border bg-card-2 p-3">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="h-4 w-4 rounded border-border accent-primary"
                checked={aiSummaryEnabled}
                onChange={(e) => setAiSummaryEnabled(e.target.checked)}
              />
              <span>启用 AI 总结</span>
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="h-4 w-4 rounded border-border accent-primary"
                checked={aiQuizEnabled}
                onChange={(e) => setAiQuizEnabled(e.target.checked)}
              />
              <span>启用 AI 出题</span>
            </label>
            <p className="text-[11px] text-muted">关闭后，该课程的课时将跳过对应的 AI 后处理（即使全局已配置）。</p>
          </div>

          <div className="flex items-center justify-between gap-2">
            {/* 预览 Prompt: CoursesContent 的 PreviewPromptModal 是私有组件,
                未导出。与其重复实现或重构成可复用组件(超出本任务范围 ——
                "不要 touch CoursesContent"),这里走 spec 的回退路径:一个
                指向 /admin/courses 的导航链接,让 admin 到课程页用现成的预览。 */}
            <button
              type="button"
              className="btn-ghost btn-sm inline-flex items-center gap-1.5"
              onClick={() => navigate('/admin/courses')}
              title="到课程管理页使用现成的 Prompt 预览"
            >
              <ExternalLink size={14} /> 到课程页预览
            </button>
            <button className="btn-primary" onClick={() => saveMut.mutate()} disabled={saveMut.isPending}>
              {saveMut.isPending ? '保存中…' : '保存课程覆盖'}
            </button>
          </div>
        </>
      ) : (
        <div className="rounded-md border border-dashed border-border bg-card-2 px-4 py-8 text-center text-sm text-muted">
          选择一个课程以覆盖其 Prompt。
        </div>
      )}
    </section>
  );
}

// ---------------- Shared section shell ----------------

function SectionShell({
  title,
  loading,
  error,
  onRetry,
}: {
  title: string;
  loading?: boolean;
  error?: string;
  onRetry?: () => void;
}) {
  return (
    <section className="space-y-3 rounded-lg border border-border bg-card p-4">
      <h2 className="text-base font-semibold">{title}</h2>
      {loading ? (
        <div className="px-1 py-6 text-sm text-muted">加载中…</div>
      ) : (
        <div className="space-y-2">
          <div className="text-sm text-bad">加载失败: {error}</div>
          {onRetry && (
            <button className="btn-secondary btn-sm" onClick={onRetry}>
              重试
            </button>
          )}
        </div>
      )}
    </section>
  );
}
