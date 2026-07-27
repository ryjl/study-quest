// PreviewPromptModal — 课程 AI Prompt 的即时预览。从 CoursesContent 抽出(原本是
// 私有 function),让课程工作台的 Prompt tab 也能用——修复旧版"编辑完 prompt 要跳
// 到课程页才能预览"的断层。
//
// 选一个 agent(summary/quiz/advice),调预览端点展示该课程最终会拼出的完整
// system+user prompt(不调 LLM,纯文本拼装)。admin 调优 hint 后立刻看效果。
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { Modal } from '../ui';

type PreviewAgent = 'summary' | 'quiz' | 'advice';
const PREVIEW_AGENTS: { key: PreviewAgent; label: string; desc: string }[] = [
  { key: 'summary', label: '总结', desc: 'summary agent' },
  { key: 'quiz', label: '出题', desc: 'quiz agent' },
  { key: 'advice', label: '建议', desc: 'advice agent' },
];

export function PreviewPromptModal({
  courseId,
  courseTitle,
  onClose,
}: {
  courseId: number;
  courseTitle?: string;
  onClose: () => void;
}) {
  const [agent, setAgent] = useState<PreviewAgent>('summary');
  const q = useQuery({
    queryKey: ['preview-prompt', courseId, agent],
    queryFn: () => api.previewCoursePrompt(courseId, agent),
    retry: false,
  });
  const err = q.error as Error | undefined;
  return (
    <Modal open onClose={onClose} title={`预览 AI Prompt${courseTitle ? ` — ${courseTitle}` : ''}`} size="xl">
      <div className="space-y-3 p-5 pt-2">
        {/* Agent 切换 chips */}
        <div className="flex flex-wrap items-center gap-1.5">
          {PREVIEW_AGENTS.map((a) => (
            <button
              key={a.key}
              onClick={() => setAgent(a.key)}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                agent === a.key ? 'bg-txt text-bg' : 'text-muted hover:bg-card-2 hover:text-txt'
              }`}
              title={a.desc}
            >
              {a.label}
            </button>
          ))}
          <span className="ml-auto text-[11px] text-muted">不调 LLM,纯本地 prompt 拼装</span>
        </div>

        {q.isLoading && <div className="py-8 text-center text-sm text-muted">加载中…</div>}
        {err && <div className="rounded-md border border-border bg-card-2 p-3 text-sm text-bad">{err.message}</div>}

        {q.data && (
          <>
            {/* resolved_hints:展示解析结果,让 admin 看到"现在生效的是学科默认还是课程覆盖"。 */}
            <div>
              <div className="mb-1 text-xs font-medium text-muted">解析后的 hints(课程级覆盖学科级)</div>
              <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
                <HintBlock label="WhisperHint" value={q.data.resolved_hints.whisper_hint} />
                <HintBlock label="SummaryHint" value={q.data.resolved_hints.summary_hint} />
                <HintBlock label="QuizHint" value={q.data.resolved_hints.quiz_hint} />
                <HintBlock label="AdviceHint" value={q.data.resolved_hints.advice_hint} />
                <HintBlock label="TermDict" value={q.data.resolved_hints.term_dict} fullWidth />
              </div>
            </div>

            <div>
              <div className="mb-1 text-xs font-medium text-muted">System Prompt(代码常量)</div>
              <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded-lg border border-border bg-card-2 p-3 text-[11px] text-txt">{q.data.system_prompt || '(空)'}</pre>
            </div>
            <div>
              <div className="mb-1 text-xs font-medium text-muted">User Prompt(含注入的 hint/TermDict)</div>
              <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded-lg border border-border bg-card-2 p-3 text-[11px] text-txt">{q.data.user_prompt || '(空)'}</pre>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}

// HintBlock:resolved hint 的小卡片。空值也显示(让 admin 看清"这个字段当前没配")。
function HintBlock({ label, value, fullWidth }: { label: string; value: string; fullWidth?: boolean }) {
  return (
    <div className={`rounded-md border border-border bg-card-2 p-2 ${fullWidth ? 'sm:col-span-2' : ''}`}>
      <div className="text-[10px] text-muted">{label}</div>
      <div className="mt-0.5 whitespace-pre-wrap break-words text-[11px] text-txt">{value || <span className="text-muted">(空)</span>}</div>
    </div>
  );
}
