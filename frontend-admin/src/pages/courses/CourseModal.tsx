import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { type Course } from '../../lib/types';
import { useSubjects } from '../../lib/useSubjects';
import { Modal } from '../../components/ui';
import { GradePicker } from '../../components/inputs';
import { TagInput } from '../../components/TagInput';
import { ImageUpload } from '../../components/inputs';
import { useToast } from '../../lib/toast';
import { getSubjectTemplate } from '../../lib/aiHintTemplates';
import { BookOpen, Film, Wand2 } from 'lucide-react';

export function CreateEditCourseModal({
  open,
  course,
  onClose,
  onSaved,
}: {
  open: boolean;
  course?: Course;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = !!course;
  const qc = useQueryClient();
  const toast = useToast();
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];

  const [title, setTitle] = useState('');
  const [grade, setGrade] = useState('');
  const [subject, setSubject] = useState('');
  const [contentType, setContentType] = useState<'learning' | 'entertainment'>('learning');
  const [coverUrl, setCoverUrl] = useState('');
  const [tagIDs, setTagIDs] = useState<number[]>([]);
  // AI 提示拆成两部分：WhisperHint 喂字幕转录（术语/口音），QuizHint 喂出题/总结
  // LLM（出题偏好 + 术语纠错字典）。后端存进单一 JSON 列 AIConfigJSON，这里拆开
  // 两个 textarea 给 admin 编辑。详见 backend model.Course.AIConfig。
  const [whisperHint, setWhisperHint] = useState('');
  const [quizHint, setQuizHint] = useState('');
  const [aiSummaryEnabled, setAiSummaryEnabled] = useState(true);
  const [aiQuizEnabled, setAiQuizEnabled] = useState(true);

  // Resync ALL form fields only when the modal (re)opens or switches to a
  // different course. We intentionally do NOT depend on `subjects` here: the
  // subject catalog loads asynchronously, and a mid-edit catalog arrival used
  // to reset a subject the admin had just picked (the effect re-ran on every
  // subjects change, re-seeding subject from subjects[0]). Instead, the
  // subject fallback to subjects[0] is applied ONCE at open via the inline
  // read of the current `subjects` prop; later catalog updates don't disturb
  // the form. The <select> below reads `subjects` reactively for its options,
  // so newly-arrived subjects still appear in the dropdown.
  useEffect(() => {
    if (open) {
      setTitle(course?.title ?? '');
      // Backend DTO sends `grades` (plural, see admin_dto.go); the legacy
      // `grade` singular alias may still appear on older rows. Fall back so
      // editing an existing course preserves its grade selection instead of
      // silently clearing it (the prior bug: reading only `grade` always got
      // "" because the backend stopped sending that field).
      setGrade(course?.grades ?? course?.grade ?? '');
      const ct = (course?.content_type === 'entertainment' ? 'entertainment' : 'learning') as 'learning' | 'entertainment';
      setContentType(ct);
      // Entertainment courses are pinned to the "entertainment" subject.
      setSubject(ct === 'entertainment' ? 'entertainment' : (course?.subject ?? subjects[0]?.key ?? ''));
      setCoverUrl(course?.cover_url ?? '');
      setTagIDs(course?.tag_ids ?? []);
      // 优先读新的 whisper_hint/quiz_hint；老课程只有 ai_hint 时，把它整体放进
      // whisperHint（保守：老字段语义偏 whisper），admin 重存后即迁移到 JSON。
      setWhisperHint(course?.whisper_hint ?? (course?.ai_hint ?? ''));
      setQuizHint(course?.quiz_hint ?? '');
      // AI switches default OFF when unset — AI is an opt-in add-on layer; a
      // course with no explicit setting behaves as plain video viewing (no AI
      // surfaces). Matches the backend gorm:"default:false".
      setAiSummaryEnabled(course?.ai_summary_enabled ?? false);
      setAiQuizEnabled(course?.ai_quiz_enabled ?? false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, course]);

  const isEntertainment = contentType === 'entertainment';
  // 当前选中科目对应的 label（用于"应用科目模板"匹配）。entertainment 无模板。
  const selectedSubjectLabel = subjects.find((s) => s.key === subject)?.label ?? '';

  const saveMut = useMutation({
    mutationFn: async () => {
      if (!title.trim()) throw new Error('请输入课程名称');
      const grades = grade.split(',').map((g) => g.trim()).filter(Boolean);
      if (grades.length === 0) throw new Error('请至少选择一个适用年级');
      const body = {
        title: title.trim(),
        // Send BOTH the new `grades` field (what the backend reads — see
        // admin_content.go parseGrades) and the legacy `grade` alias for
        // backward compatibility with any older middleware path.
        grades: grade,
        grade,
        subject: isEntertainment ? 'entertainment' : subject,
        content_type: contentType,
        cover_url: coverUrl,
        tag_ids: tagIDs,
        // 后端读 whisper_hint / quiz_hint（存进 AIConfigJSON 单列）。不再写老 ai_hint。
        whisper_hint: whisperHint.trim(),
        quiz_hint: quizHint.trim(),
        ai_summary_enabled: aiSummaryEnabled,
        ai_quiz_enabled: aiQuizEnabled,
      };
      if (isEdit && course) return api.updateCourse(course.id, body);
      return api.createCourse(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '课程已更新' : '课程已创建');
      qc.invalidateQueries({ queryKey: ['courses'] });
      onSaved();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <Modal open={open} onClose={onClose} title={isEdit ? '编辑课程' : '新增课程库'} size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">课程名称</label>
          <input className="input" placeholder="如：神奇的物理世界" value={title} onChange={(e) => setTitle(e.target.value)} required autoFocus />
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted">适用年级（可多选）</label>
          <GradePicker value={grade} onChange={setGrade} />
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted">内容类型</label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => { setContentType('learning'); setSubject(course?.subject ?? subjects[0]?.key ?? ''); }}
              className={`flex flex-1 items-center justify-center gap-1.5 rounded-md border px-3 py-2 text-sm transition-colors ${!isEntertainment ? 'border-txt bg-card-2 text-txt font-medium' : 'border-border text-muted hover:text-txt'}`}
            >
              <BookOpen size={14} /> 学习
            </button>
            <button
              type="button"
              onClick={() => { setContentType('entertainment'); setSubject('entertainment'); }}
              className={`flex flex-1 items-center justify-center gap-1.5 rounded-md border px-3 py-2 text-sm transition-colors ${isEntertainment ? 'border-txt bg-card-2 text-txt font-medium' : 'border-border text-muted hover:text-txt'}`}
            >
              <Film size={14} /> 娱乐
            </button>
          </div>
        </div>

        {!isEntertainment && (
          <div>
            <label className="mb-1 block text-xs text-muted">类别 / 科目</label>
            <select className="input" value={subject} onChange={(e) => setSubject(e.target.value)}>
              {subjects.filter((s) => s.key !== 'entertainment').map((s) => (
                <option key={s.key} value={s.key}>
                  {s.label} ({s.key})
                </option>
              ))}
            </select>
          </div>
        )}

        <ImageUpload label="封面图" value={coverUrl} onChange={setCoverUrl} />

        <div>
          <label className="mb-1 block text-xs text-muted">标签</label>
          <TagInput value={tagIDs} onChange={setTagIDs} />
        </div>

        <div className="space-y-3 rounded-xl border border-border bg-card-2 p-3">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-muted">AI 提示（可选）</span>
            {!isEntertainment && (
              <button
                type="button"
                onClick={() => {
                  const tpl = getSubjectTemplate(selectedSubjectLabel);
                  if (!tpl) {
                    toast.error(`「${selectedSubjectLabel}」暂无模板，请手动填写`);
                    return;
                  }
                  setWhisperHint(tpl.whisperHint);
                  setQuizHint(tpl.quizHint);
                  toast.success(`已套用「${selectedSubjectLabel}」模板，可继续微调`);
                }}
                className="flex items-center gap-1 rounded-md border border-border px-2 py-1 text-[11px] text-muted transition-colors hover:border-primary hover:text-primary"
                title="按当前科目填入默认提示，可继续微调"
              >
                <Wand2 size={12} /> 套用{selectedSubjectLabel || '科目'}模板
              </button>
            )}
          </div>

          <div>
            <label className="mb-1 block text-[11px] text-muted">Whisper 提示（喂字幕转录，术语/口音，≤240 字）</label>
            <textarea
              className="input min-h-[56px] resize-y"
              placeholder="如：象棋术语：车马炮兵卒将帅士仕相象，屏风马，中炮。老师带南方口音。"
              value={whisperHint}
              onChange={(e) => setWhisperHint(e.target.value)}
            />
            <p className="mt-1 text-[11px] text-muted">拼入 Whisper 的 initial_prompt，压制学科术语同音错字。过长会被截断到 240 字。</p>
          </div>

          <div>
            <label className="mb-1 block text-[11px] text-muted">出题提示（喂总结/出题/建议 LLM：题型偏好 + 术语纠错字典）</label>
            <textarea
              className="input min-h-[80px] resize-y"
              placeholder={'如：题型倾向：计算题 ≥50% 出填空；难度偏难。\n术语字典：车（勿作居）、和棋（勿作合棋）。'}
              value={quizHint}
              onChange={(e) => setQuizHint(e.target.value)}
            />
            <p className="mt-1 text-[11px] text-muted">LLM 会按这里的偏好出题，并在输出时按术语字典纠正字幕错字（只改输出，不改字幕本身）。</p>
          </div>
        </div>

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

        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '保存并创建'}
        </button>
      </form>
    </Modal>
  );
}
