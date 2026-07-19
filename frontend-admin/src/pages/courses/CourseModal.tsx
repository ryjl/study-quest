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
  // AI 提示拆成 5 个独立维度(镜像后端 model.AIConfig),存进单一 JSON 列
  // AIConfigJSON。详见 backend model.Course.AIConfig / Course.Effective*Hint。
  //   - whisperHint 喂字幕转录(术语/口音)
  //   - summaryHint 喂 AI 总结(风格/侧重点)
  //   - quizHint    喂出题 LLM(题型偏好/难度)
  //   - adviceHint  喂建议 LLM(建议侧重点)
  //   - termDict    横切给总结/出题/建议的术语纠错字典
  const [whisperHint, setWhisperHint] = useState('');
  const [summaryHint, setSummaryHint] = useState('');
  const [quizHint, setQuizHint] = useState('');
  const [adviceHint, setAdviceHint] = useState('');
  const [termDict, setTermDict] = useState('');
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
      // 优先读 5 字段的 ai_config;老课程只有顶层 whisper_hint/quiz_hint(或更老的
      // ai_hint)时回退——admin 重存后即迁移到 ai_config JSON。
      const cfg = course?.ai_config;
      setWhisperHint(cfg?.whisper_hint ?? course?.whisper_hint ?? (course?.ai_hint ?? ''));
      setSummaryHint(cfg?.summary_hint ?? '');
      setQuizHint(cfg?.quiz_hint ?? course?.quiz_hint ?? '');
      setAdviceHint(cfg?.advice_hint ?? '');
      setTermDict(cfg?.term_dict ?? '');
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
        // 后端读 ai_config 对象(5 字段,存进 AIConfigJSON 单列)。不再写老 ai_hint
        // 或顶层 whisper_hint/quiz_hint(顶层仅老表单兼容绑定,新表单统一走 ai_config)。
        ai_config: {
          whisper_hint: whisperHint.trim(),
          summary_hint: summaryHint.trim(),
          quiz_hint: quizHint.trim(),
          advice_hint: adviceHint.trim(),
          term_dict: termDict.trim(),
        },
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
                  // 优先从后端读当前选中 Subject 的 ai_config(模板现在存 DB 学科级)。
                  // subjects 来自 useSubjects() 的实时缓存。找不到 / 学科 ai_config
                  // 全空时,回退到前端内置的 getSubjectTemplate(老 fallback,保留)。
                  const subj = subjects.find((s) => s.key === subject);
                  const dbCfg = subj?.ai_config;
                  const hasDb =
                    !!dbCfg &&
                    (!!dbCfg.whisper_hint?.trim() ||
                      !!dbCfg.summary_hint?.trim() ||
                      !!dbCfg.quiz_hint?.trim() ||
                      !!dbCfg.advice_hint?.trim() ||
                      !!dbCfg.term_dict?.trim());
                  if (hasDb) {
                    setWhisperHint(dbCfg!.whisper_hint ?? '');
                    setSummaryHint(dbCfg!.summary_hint ?? '');
                    setQuizHint(dbCfg!.quiz_hint ?? '');
                    setAdviceHint(dbCfg!.advice_hint ?? '');
                    setTermDict(dbCfg!.term_dict ?? '');
                    toast.success(`已套用「${selectedSubjectLabel}」学科默认模板，可继续微调`);
                    return;
                  }
                  // 学科 ai_config 为空:回退到前端内置模板(只填 whisper/quiz 两字段)。
                  const tpl = getSubjectTemplate(selectedSubjectLabel);
                  if (!tpl) {
                    toast.error(
                      `「${selectedSubjectLabel}」暂无默认模板，请手动填写或先到「学科管理」配置该学科的 AI 提示`,
                    );
                    return;
                  }
                  setWhisperHint(tpl.whisperHint);
                  setQuizHint(tpl.quizHint);
                  toast.success(`已套用「${selectedSubjectLabel}」内置模板（学科未配置 DB 默认），可继续微调`);
                }}
                className="flex items-center gap-1 rounded-md border border-border px-2 py-1 text-[11px] text-muted transition-colors hover:border-primary hover:text-primary"
                title="按当前科目填入学科默认 AI 提示（优先读后端学科配置，回退到内置模板）"
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
            <label className="mb-1 block text-[11px] text-muted">总结提示（喂 AI 总结：风格/侧重点）</label>
            <textarea
              className="input min-h-[56px] resize-y"
              placeholder="如：侧重开局原理，多举例题，避免堆砌术语。"
              value={summaryHint}
              onChange={(e) => setSummaryHint(e.target.value)}
            />
            <p className="mt-1 text-[11px] text-muted">留空则回退到学科级默认（若学科也未配则为空，等同无指引）。</p>
          </div>

          <div>
            <label className="mb-1 block text-[11px] text-muted">出题提示（喂出题 LLM：题型偏好/难度/出题指引）</label>
            <textarea
              className="input min-h-[64px] resize-y"
              placeholder={'如：题型倾向：计算题 ≥50% 出填空；难度偏难。'}
              value={quizHint}
              onChange={(e) => setQuizHint(e.target.value)}
            />
            <p className="mt-1 text-[11px] text-muted">LLM 会按这里的偏好出题。留空则回退到学科级默认。</p>
          </div>

          <div>
            <label className="mb-1 block text-[11px] text-muted">建议提示（喂建议 LLM：建议侧重点/口吻）</label>
            <textarea
              className="input min-h-[56px] resize-y"
              placeholder="如：象棋重实战练习，多鼓励；数学重计算巩固。"
              value={adviceHint}
              onChange={(e) => setAdviceHint(e.target.value)}
            />
            <p className="mt-1 text-[11px] text-muted">留空则回退到学科级默认。</p>
          </div>

          <div>
            <label className="mb-1 block text-[11px] text-muted">术语字典（横切给总结/出题/建议：纠正字幕同音错字）</label>
            <textarea
              className="input min-h-[56px] resize-y"
              placeholder={'如：车（勿作居）、和棋（勿作合棋）、通分（勿作同分）。'}
              value={termDict}
              onChange={(e) => setTermDict(e.target.value)}
            />
            <p className="mt-1 text-[11px] text-muted">LLM 输出时按此字典纠正字幕错字（只改输出，不改字幕本身）。课程级会追加到学科级后面合并生效。</p>
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
