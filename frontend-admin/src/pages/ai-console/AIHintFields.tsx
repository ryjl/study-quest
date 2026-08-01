import { Wand2 } from 'lucide-react';

// AIHintFields — the 5-dimension AI prompt textarea block, shared between
// CourseModal (course override), SubjectModal (subject default), and the
// AI Console's PromptConfigTab. Centralizing this keeps labels, helpers, and
// the "apply template" button visually identical everywhere, so admins learn
// the surface once.
//
// The shape mirrors backend model.AIConfig (5 string fields stored as one
// JSON blob). The `value`/`onChange` contract is intentionally a plain object
// (not individual props) so a parent that already has `{whisper_hint, ...}`
// in state can bind it in one go — same shape CourseModal builds on save.
//
// Note on `showApplyTemplateButton`: only the course-override surface needs
// it (subjects ARE the template source, so the button would be circular).
// The actual click behavior is delegated via `onApplyTemplate` — the parent
// knows about subjects/labels and decides what "apply template" means
// (复制选中课程的学科 ai_config 到当前编辑的 5 字段)。模板源 100% 来自 DB
// Subject.AIConfig(象棋等模板在后端 SeedDefaultSubjects 的 subjectAISeed)。

export interface AiHintFieldsValue {
  whisper_hint: string;
  summary_hint: string;
  quiz_hint: string;
  advice_hint: string;
  term_dict: string;
}

export function emptyAiHintValue(): AiHintFieldsValue {
  return { whisper_hint: '', summary_hint: '', quiz_hint: '', advice_hint: '', term_dict: '' };
}

export interface AIHintFieldsProps {
  value: AiHintFieldsValue;
  onChange: (next: AiHintFieldsValue) => void;
  /** Show the "套用模板" button above the textareas. Only meaningful for the
   * course-override surface; the click is delegated to the parent. */
  showApplyTemplateButton?: boolean;
  onApplyTemplate?: () => void;
  applyTemplateLabel?: string;
}

const FIELD_LABELS = {
  whisper_hint: 'Whisper 提示（喂字幕转录，术语/口音，≤240 字）',
  summary_hint: '总结提示（喂 AI 总结：风格/侧重点）',
  quiz_hint: '出题提示（喂出题 LLM：题型偏好/难度/出题指引）',
  advice_hint: '建议提示（喂建议 LLM：建议侧重点/口吻）',
  term_dict: '术语字典（横切给总结/出题/建议：纠正字幕同音错字）',
} as const;

const PLACEHOLDERS = {
  whisper_hint: '如：象棋术语：车马炮兵卒将帅士仕相象，屏风马，中炮。老师带南方口音。',
  summary_hint: '如：侧重开局原理，多举例题，避免堆砌术语。',
  quiz_hint: '如：题型倾向：计算题 ≥50% 出填空；难度偏难。',
  advice_hint: '如：象棋重实战练习，多鼓励；数学重计算巩固。',
  term_dict: '如：车（勿作居）、和棋（勿作合棋）、通分（勿作同分）。',
} as const;

const HELPERS = {
  whisper_hint: '拼入 Whisper 的 initial_prompt，压制学科术语同音错字。过长会被截断到 240 字。',
  summary_hint: '留空则回退到学科级默认（若学科也未配则为空，等同无指引）。',
  quiz_hint: 'LLM 会按这里的偏好出题。留空则回退到学科级默认。',
  advice_hint: '留空则回退到学科级默认。',
  term_dict: 'LLM 输出时按此字典纠正字幕错字（只改输出，不改字幕本身）。课程级会追加到学科级后面合并生效。',
} as const;

const TEXTAREA_MIN_HEIGHT: Record<keyof AiHintFieldsValue, string> = {
  whisper_hint: 'min-h-[56px]',
  summary_hint: 'min-h-[56px]',
  quiz_hint: 'min-h-[64px]',
  advice_hint: 'min-h-[56px]',
  term_dict: 'min-h-[56px]',
};

const FIELD_ORDER: (keyof AiHintFieldsValue)[] = ['whisper_hint', 'summary_hint', 'quiz_hint', 'advice_hint', 'term_dict'];

export function AIHintFields({
  value,
  onChange,
  showApplyTemplateButton,
  onApplyTemplate,
  applyTemplateLabel,
}: AIHintFieldsProps) {
  const setField = (k: keyof AiHintFieldsValue, v: string) => onChange({ ...value, [k]: v });
  return (
    <div className="space-y-3 rounded-xl border border-border bg-card-2 p-3">
      {showApplyTemplateButton && (
        <div className="flex items-center justify-between">
          <span className="text-xs font-medium text-muted">AI 提示（可选）</span>
          <button
            type="button"
            onClick={onApplyTemplate}
            className="flex items-center gap-1 rounded-md border border-border px-2 py-1 text-[11px] text-muted transition-colors hover:border-primary hover:text-primary"
            title="按当前科目填入学科默认 AI 提示"
          >
            <Wand2 size={12} /> {applyTemplateLabel || '套用模板'}
          </button>
        </div>
      )}
      {FIELD_ORDER.map((k) => (
        <div key={k}>
          <label className="mb-1 block text-[11px] text-muted">{FIELD_LABELS[k]}</label>
          <textarea
            className={`input ${TEXTAREA_MIN_HEIGHT[k]} resize-y`}
            placeholder={PLACEHOLDERS[k]}
            value={value[k]}
            onChange={(e) => setField(k, e.target.value)}
          />
          <p className="mt-1 text-[11px] text-muted">{HELPERS[k]}</p>
        </div>
      ))}
    </div>
  );
}
