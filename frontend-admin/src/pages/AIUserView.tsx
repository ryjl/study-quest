import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { AiQuizDetail, AiTraceStep } from '../lib/types';
import { Modal } from '../components/ui';

// AIUserView — the per-student observability page. Pick a user (from a
// searchable dropdown of all users), see their generated quizzes, drill into
// one to see: the questions WITH answers, the student's answer history, their
// mastery per chunk, the agent's feedback (its analysis of the student), and
// the reasoning trace that produced it.
//
// This is where you watch the feedback loop in action: answer → mastery updates
// → next generation adapts. It complements the job-centric AIWorkflow page
// (which shows generation jobs across all students).

export function AIUserView() {
  const [userId, setUserId] = useState<number | null>(null);
  const [query, setQuery] = useState(''); // filters the user dropdown by nickname
  const [openQuizId, setOpenQuizId] = useState<number | null>(null);

  // The user list drives the picker. Carried once on mount; typically small
  // (a family-scale install has a handful of users) so a client-filtered
  // datalist is enough — no server-side search round-trip needed.
  const usersQ = useQuery({ queryKey: ['users'], queryFn: api.listUsers });
  const users = usersQ.data ?? [];

  const quizzesQ = useQuery({
    queryKey: ['ai-user-quizzes', userId],
    queryFn: () => api.listUserQuizzes(userId!),
    enabled: userId != null,
  });

  const quizzes = quizzesQ.data ?? [];
  const selectedUser = useMemo(() => users.find((u) => u.id === userId) ?? null, [users, userId]);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">AI 用户视图</h1>
        <p className="mt-1 text-sm text-muted">
          按学生查看 AI 学习情况:生成的题库、答题记录、掌握度(memory)、以及 agent 对这个学生的评价与出题思考过程。
          这是观察反馈循环的地方——答题更新 memory,下次出题自适应。
        </p>
      </div>

      {/* User picker — a searchable combobox over api.listUsers(). An <input>
          filters by nickname; a native <datalist> offers matches. Picking one
          sets the user id; a small "查看" button makes the selection explicit
          (datalist selection also updates via onChange). */}
      <div className="flex flex-wrap items-center gap-2">
        <input
          className="input max-w-[260px]"
          list="ai-user-options"
          placeholder={usersQ.isLoading ? '加载用户…' : '搜索昵称选择用户'}
          value={selectedUser ? selectedUser.nickname : query}
          onChange={(e) => {
            const entered = e.target.value;
            // Match a known user by nickname (exact) → select; otherwise treat
            // as free-text filter so the list narrows as they type.
            const match = users.find((u) => u.nickname === entered);
            if (match) {
              setUserId(match.id);
              setQuery('');
            } else {
              setUserId(null);
              setQuery(entered);
            }
          }}
        />
        <datalist id="ai-user-options">
          {users
            .filter((u) => (query ? u.nickname.toLowerCase().includes(query.toLowerCase()) : true))
            .map((u) => (
              <option key={u.id} value={u.nickname}>
                {u.role}
              </option>
            ))}
        </datalist>
      </div>

      {/* Quiz list */}
      <div className="space-y-3">
        <h2 className="text-base font-semibold">
          {selectedUser ? `正在查看:${selectedUser.nickname}` : '题库列表'}
        </h2>
        {userId == null ? (
          <div className="rounded-2xl border border-border bg-card px-4 py-10 text-center text-sm text-muted">
            选择一个用户查看
          </div>
        ) : quizzesQ.isLoading ? (
          <div className="rounded-2xl border border-border bg-card px-4 py-10 text-center text-sm text-muted">加载中…</div>
        ) : quizzes.length === 0 ? (
          <div className="rounded-2xl border border-border bg-card px-4 py-10 text-center text-sm text-muted">该用户暂无题库</div>
        ) : (
          <div className="overflow-hidden rounded-2xl border border-border bg-card">
            <table className="w-full text-sm">
              <thead className="border-b border-border bg-card-2 text-xs text-muted">
                <tr>
                  <th className="px-4 py-3 text-left font-medium">题库 ID</th>
                  <th className="px-4 py-3 text-left font-medium">Episode</th>
                  <th className="px-4 py-3 text-left font-medium">课程</th>
                  <th className="px-4 py-3 text-left font-medium">难度</th>
                  <th className="px-4 py-3 text-left font-medium">生成时间</th>
                  <th className="px-4 py-3 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {quizzes.map((q) => (
                  <tr key={q.id} className="border-b border-border/60 last:border-0 hover:bg-card-2/50">
                    <td className="px-4 py-3 font-medium">#{q.id}</td>
                    <td className="px-4 py-3">
                      <span className="text-txt">{q.episode_title || `#${q.episode_id}`}</span>
                    </td>
                    <td className="px-4 py-3 text-muted">{q.course_title || `课程 ${q.course_id}`}</td>
                    <td className="px-4 py-3 text-muted">{q.difficulty}</td>
                    <td className="px-4 py-3 text-xs text-muted">{q.created_at}</td>
                    <td className="px-4 py-3 text-right">
                      <button className="btn-ghost btn-sm" onClick={() => setOpenQuizId(q.id)}>
                        查看详情
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Quiz detail modal */}
      <QuizDetailModal quizId={openQuizId} onClose={() => setOpenQuizId(null)} />
    </div>
  );
}

function QuizDetailModal({ quizId, onClose }: { quizId: number | null; onClose: () => void }) {
  const q = useQuery({
    queryKey: ['ai-quiz-detail', quizId],
    queryFn: () => api.getQuizDetail(quizId!),
    enabled: quizId != null,
  });
  const detail: AiQuizDetail | undefined = q.data;

  return (
    <Modal open={quizId != null} onClose={onClose} title={detail ? `题库 #${quizId} 详情` : '加载中…'} size="xl">
      {!detail ? (
        <div className="px-4 py-10 text-center text-sm text-muted">{q.isLoading ? '加载中…' : '无数据'}</div>
      ) : (
        <div className="space-y-5">
          {/* Agent feedback — the LLM's analysis of this student. The headline
              observability artifact: the agent read the student's memory and
              produced this assessment + study advice. */}
          {detail.quiz.agent_feedback && (
            <div className="rounded-xl border border-blue-500/30 bg-blue-500/5 p-3">
              <div className="mb-1 text-xs font-medium text-blue-600">Agent 对这个学生的评价与建议</div>
              <p className="text-sm text-txt whitespace-pre-wrap">{detail.quiz.agent_feedback}</p>
            </div>
          )}

          {/* Mastery snapshot — the feedback-loop state the agent read/writes. */}
          {detail.masteries.length > 0 && (
            <div>
              <div className="mb-1 text-xs font-medium text-muted">掌握度 (memory)</div>
              <div className="space-y-1">
                {detail.masteries.map((m) => (
                  <MasteryBar key={m.id} mastery={m.mastery} chunk={m.chunk_id} correct={m.correct_count} wrong={m.wrong_count} />
                ))}
              </div>
            </div>
          )}

          {/* Questions with answers (admin sees the answer key). */}
          <div>
            <div className="mb-1 text-xs font-medium text-muted">题目 ({detail.questions.length})</div>
            <ol className="space-y-2">
              {detail.questions.map((q, i) => (
                <QuestionCard key={q.id} index={i} q={q} answers={detail.answers.filter((a) => a.question_id === q.id)} />
              ))}
            </ol>
          </div>

          {/* The agent's reasoning trace from the run that generated this quiz. */}
          {detail.runs.some((r) => r.trace_json) && <TraceTimeline runs={detail.runs} />}
        </div>
      )}
    </Modal>
  );
}

function MasteryBar({ mastery, chunk, correct, wrong }: { mastery: number; chunk: number; correct: number; wrong: number }) {
  const pct = Math.round(mastery * 100);
  const tone = mastery < 0.4 ? 'bg-bad' : mastery < 0.7 ? 'bg-amber-500' : 'bg-emerald-500';
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="w-20 text-muted">片段#{chunk}</span>
      <div className="h-2 flex-1 overflow-hidden rounded-full bg-card-2">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${pct}%` }} />
      </div>
      <span className="w-16 text-right font-mono text-muted">{pct}%</span>
      <span className="w-20 text-right text-emerald-600">对{correct}</span>
      <span className="w-20 text-right text-bad">错{wrong}</span>
    </div>
  );
}

function QuestionCard({ index, q, answers }: { index: number; q: AiQuizDetail['questions'][number]; answers: AiQuizDetail['answers'] }) {
  let options: string[] = [];
  try {
    if (q.options) options = JSON.parse(q.options);
  } catch {
    /* keep empty */
  }
  let acceptText: string[] = [];
  try {
    if (q.answer_text) acceptText = JSON.parse(q.answer_text);
  } catch {
    /* keep empty */
  }
  const typeLabel = q.type === 'fill' ? '填空' : '选择';
  return (
    <li className="rounded-xl border border-border bg-card-2 p-3">
      <div className="flex items-center gap-2">
        <span className="text-xs font-medium text-muted">第{index + 1}题</span>
        <span className="rounded-full bg-card px-2 py-0.5 text-[10px] text-muted">{typeLabel}</span>
      </div>
      <div className="mt-1 text-sm text-txt">{q.stem}</div>
      {q.type !== 'fill' && (
        <ul className="mt-1 space-y-0.5 text-xs">
          {options.map((o, i) => (
            <li key={i} className={i === q.answer ? 'text-emerald-600' : 'text-muted'}>
              {String.fromCharCode(65 + i)}. {o} {i === q.answer && '✓'}
            </li>
          ))}
        </ul>
      )}
      {q.type === 'fill' && <div className="mt-1 text-xs text-emerald-600">答案: {acceptText.join(' / ')}</div>}
      {q.explanation && <div className="mt-1 text-xs text-muted">解析: {q.explanation}</div>}
      {q.chunk_start_time != null && <div className="mt-1 text-[11px] text-blue-600">视频位置: {fmtSec(q.chunk_start_time)}</div>}
      {/* Answer history — shows each attempt (redo adds rows). */}
      {answers.length > 0 && (
        <div className="mt-2 border-t border-border/50 pt-1.5 text-[11px]">
          <span className="text-muted">答题记录: </span>
          {answers.map((a, i) => (
            <span key={a.id} className={a.correct ? 'text-emerald-600' : 'text-bad'}>
              {i > 0 && '、'}
              {q.type !== 'fill' ? String.fromCharCode(65 + a.user_answer) : `#${a.user_answer}`}({a.correct ? '对' : '错'})
            </span>
          ))}
        </div>
      )}
    </li>
  );
}

function TraceTimeline({ runs }: { runs: AiQuizDetail['runs'] }) {
  // Find the run that carries the trace (capability=quiz with trace_json).
  const tracedRun = runs.find((r) => r.trace_json);
  if (!tracedRun) return null;
  let steps: AiTraceStep[] = [];
  try {
    steps = JSON.parse(tracedRun.trace_json!);
  } catch {
    return null;
  }
  return (
    <div>
      <div className="mb-1 text-xs font-medium text-muted">出题思考时间线 (agent ReAct 循环)</div>
      <ol className="space-y-2">
        {steps.map((s) => (
          <li key={s.step} className="rounded-xl border border-border bg-card-2 p-3 text-xs">
            <div className="flex items-center gap-2">
              <span className="font-mono text-[11px] text-muted">#{s.step}</span>
              <span className="font-medium text-txt">{s.thought}</span>
              {s.is_final && <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-[10px] text-emerald-600">最终</span>}
            </div>
            {s.action && (
              <div className="mt-1 font-mono text-[11px] text-blue-600">
                → {s.action.tool}({s.action.args || ''})
              </div>
            )}
            {s.observation && <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap text-[11px] text-muted">{s.observation}</pre>}
          </li>
        ))}
      </ol>
    </div>
  );
}

function fmtSec(s: number): string {
  const m = Math.floor(s / 60);
  const sec = s % 60;
  return `${m}:${sec.toString().padStart(2, '0')}`;
}
