import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Check } from 'lucide-react';
import { api } from '../lib/api';
import type { AiQuizDetail, AiTraceStep } from '../lib/types';
import { fmtSec } from '../lib/format';
import { pollWhileGenerating } from '../lib/query';
import { Modal } from '../components/ui';
import { PageHeader } from '../components/PageHeader';
import { MiniMarkdown } from '../components/ai/MiniMarkdown';

// AIUserView — the per-student observability page. Pick a user (from a
// searchable dropdown of all users), see their generated quizzes, drill into
// one to see: the questions WITH answers, the student's answer history, their
// mastery per chunk, the agent's feedback (its analysis of the student), and
// the reasoning trace that produced it.
//
// This is where you watch the feedback loop in action: answer → mastery updates
// → next generation adapts. It complements the job-centric AIWorkflow page
// (which shows generation jobs across all students).

// embedded=true 时不渲染自己的 PageHeader —— 由父页面(学生工作台)提供统一标题。
// userId prop(可选):外部传入=学生工作台模式,学生已由路由固定,不渲染 user picker;
// courseFilter(可选):传入时题库列表只显示该 course_id 的 quiz(学生工作台「题库与建议」
// tab 的课程过滤器联动用)。不传=显示全部。
export function AIUserView({ embedded = false, userId: lockedUserId, courseFilter }: { embedded?: boolean; userId?: number; courseFilter?: number } = {}) {
  const isLocked = lockedUserId != null;
  const [userId, setUserId] = useState<number | null>(lockedUserId ?? null);
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

  const quizzes = useMemo(() => {
    const all = quizzesQ.data ?? [];
    // courseFilter:只显示该课程的 quiz(学生工作台课程过滤器联动)。
    return courseFilter != null ? all.filter((q) => q.course_id === courseFilter) : all;
  }, [quizzesQ.data, courseFilter]);
  const selectedUser = useMemo(() => users.find((u) => u.id === userId) ?? null, [users, userId]);

  return (
    <div className="space-y-6">
      {!embedded && (
        <PageHeader
          title="AI 用户视图"
          breadcrumb={[{ label: 'AI 运营' }]}
          description="按用户查看 AI 题库、答题历史与 agent 决策回放。"
        />
      )}

      {/* User picker — 工作台模式(isLocked)下学生已由路由固定,不渲染 picker。
          独立模式下是 searchable combobox:nickname 可能重复,option value 带 (#id)。 */}
      {!isLocked && (
        <div className="flex flex-wrap items-center gap-2">
          <input
            className="input max-w-[260px]"
            list="ai-user-options"
            placeholder={usersQ.isLoading ? '加载用户…' : '搜索昵称选择用户'}
            value={selectedUser ? `${selectedUser.nickname} (#${selectedUser.id})` : query}
            onChange={(e) => {
              const entered = e.target.value;
              const idMatch = entered.match(/\(#(\d+)\)\s*$/);
              if (idMatch) {
                const id = Number(idMatch[1]);
                if (users.some((u) => u.id === id)) {
                  setUserId(id);
                  setQuery('');
                  return;
                }
              }
              setUserId(null);
              setQuery(entered);
            }}
          />
          <datalist id="ai-user-options">
            {users
              .filter((u) => (query ? u.nickname.toLowerCase().includes(query.toLowerCase()) : true))
              .map((u) => (
                <option key={u.id} value={`${u.nickname} (#${u.id})`}>
                  {u.role}
                </option>
              ))}
          </datalist>
        </div>
      )}

      {/* 用户学习报告(Phase E)—— agent 驱动的跨课程画像,供 admin 查看。
          选中用户后显示:有报告则渲染文本;无报告显示"生成"按钮;生成中显示 spinner +
          轮询(类似 quiz 的 lazy 生成)。独立成组件,内部管理触发 + 轮询状态。 */}
      {/* 学习报告:独立模式下渲染(AIConsole users tab 时代遗留)。工作台模式(isLocked)
          下不渲染——学生工作台「概览」tab 已有 UserStudyReportCard(且带删除按钮,能力更全),
          避免同一报告在两个 tab 重复且能力不一致。 */}
      {userId != null && !isLocked && <UserStudyReportSection userId={userId} />}

      {/* Quiz list */}
      <div className="space-y-3">
        <h2 className="text-base font-semibold">
          {selectedUser ? `正在查看:${selectedUser.nickname}` : '题库列表'}
        </h2>
        {userId == null ? (
          <div className="rounded-lg border border-border bg-card px-4 py-10 text-center text-sm text-muted">
            选择一个用户查看
          </div>
        ) : quizzesQ.isLoading ? (
          <div className="rounded-lg border border-border bg-card px-4 py-10 text-center text-sm text-muted">加载中…</div>
        ) : quizzes.length === 0 ? (
          <div className="rounded-lg border border-border bg-card px-4 py-10 text-center text-sm text-muted">该用户暂无题库</div>
        ) : (
          <div className="overflow-hidden rounded-lg border border-border bg-card">
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

// UserStudyReportSection — Phase E admin 用户学习报告区块。
// 三态渲染:
//   - generating:无报告(或有旧报告)+ 有在途 job → 显示 spinner + "正在生成学习报告…",
//     并用 refetchInterval 每 3s 轮询 GET 端点直到 ready(同 SubtitleQueue/AIWorkflow 的轮询模式)。
//   - ready:有报告 → 渲染报告文本(whitespace-pre-wrap 支持段落/换行)+ 生成时间 + "重新生成"按钮。
//   - '':无报告 + 未生成 → 显示"生成学习报告"按钮,点击触发 POST 然后开始轮询。
//
// 触发(triggerUserReport)后立即把 query 的数据标成 generating(乐观更新),并开启轮询——
// 这样按钮点击后无需等待下一轮 poll 就能切到 spinner。
function UserStudyReportSection({ userId }: { userId: number }) {
  const qc = useQueryClient();
  const reportQ = useQuery({
    queryKey: ['ai-user-study-report', userId],
    queryFn: () => api.getUserReport(userId),
    // 只在 generating 时轮询;ready 或空时停止(省请求)。refetchIntervalInBackground
    // 关掉,避免后台标签页无意义轮询(同 SubtitleQueue 的做法)。
    refetchInterval: pollWhileGenerating(),
    refetchIntervalInBackground: false,
  });
  const data = reportQ.data;
  const generating = data?.status === 'generating';
  const [triggering, setTriggering] = useState(false);

  const trigger = async () => {
    setTriggering(true);
    try {
      // POST 触发生成(异步入队 job)。乐观把缓存标成 generating 让 spinner 立刻出现,
      // 真实 status 由后续轮询的 GET 校准(后端可能因在途 job 返回 generating,语义一致)。
      qc.setQueryData(['ai-user-study-report', userId], { status: 'generating' });
      await api.triggerUserReport(userId);
      // 唤醒一次查询(若 refetchInterval 还没到,立即拉一次确认状态)。
      await reportQ.refetch();
    } catch {
      // 触发失败:回滚到上一态,让用户能重试。错误用 toast/alert 太重,这里清掉 generating
      // 标记即可——下次 refetch 会拿到真实的(很可能是 '')status。
      qc.invalidateQueries({ queryKey: ['ai-user-study-report', userId] });
    } finally {
      setTriggering(false);
    }
  };

  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <div className="mb-2 flex items-center justify-between">
        <h2 className="text-base font-semibold">跨课程学习报告</h2>
        {/* 任何时候都允许"重新生成"——报告是快照,admin 可刷新过期数据。
            generating/triggering 时禁用按钮防连点(后端也有在途 job 去重,这是前端第二道)。 */}
        <button
          className="btn-ghost btn-sm"
          onClick={trigger}
          disabled={generating || triggering}
          title={data?.status === 'ready' ? '重新生成(覆盖当前报告)' : '生成学习报告'}
        >
          {triggering ? '提交中…' : data?.status === 'ready' ? '重新生成' : '生成学习报告'}
        </button>
      </div>
      {generating ? (
        // 生成中:spinner + 提示。agent 跨课程分析 + ReAct 多轮,通常几十秒,提示用户稍候。
        <div className="flex items-center gap-2 px-1 py-6 text-sm text-muted">
          <span className="h-4 w-4 animate-spin rounded-full border-2 border-muted border-t-transparent" />
          正在生成学习报告…(agent 正在跨课程分析,约需数十秒)
        </div>
      ) : data?.status === 'ready' && data.report ? (
        // ready:渲染报告文本。whitespace-pre-wrap 保留段落/换行,让报告的结构清晰。
        <div className="space-y-2">
          <MiniMarkdown text={data.report} />
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
            {data.generated_at && <span>生成于 {new Date(data.generated_at).toLocaleString('zh-CN')}</span>}
            {data.model_used && <span>模型: {data.model_used}</span>}
          </div>
        </div>
      ) : (
        // 无报告未生成:占位 + 引导。数据不足时 agent 会在报告里说明,这里只是触发前的空态。
        <div className="px-1 py-6 text-sm text-muted">
          {reportQ.isLoading ? '加载中…' : '暂无学习报告。点击"生成学习报告",由 agent 分析该学生跨课程的学习情况(整体掌握度、强项弱项课程、跨课程关联、重点建议)。'}
        </div>
      )}
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
            <div className="rounded-lg border border-blue-500/30 bg-blue-500/5 p-3">
              <div className="mb-1 text-xs font-medium text-blue-600">Agent 对这个学生的评价与建议</div>
              <MiniMarkdown text={detail.quiz.agent_feedback} />
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
  // 正确答案信息全在 Scoring JSON 里。
  // choice/multi_choice:correct_index / correct_indices;fill:accept。
  // 兜底:Scoring 缺失或解析失败时,choice 无高亮、fill 无参考答案。
  let correctIndex: number | undefined;
  let acceptText: string[] = [];
  try {
    if (q.scoring) {
      const s = JSON.parse(q.scoring);
      if (typeof s.correct_index === 'number') correctIndex = s.correct_index;
      if (Array.isArray(s.accept)) acceptText = s.accept;
    }
  } catch {
    /* keep empty */
  }
  const typeLabel = q.type === 'fill' ? '填空' : '选择';
  return (
    <li className="rounded-lg border border-border bg-card-2 p-3">
      <div className="flex items-center gap-2">
        <span className="text-xs font-medium text-muted">第{index + 1}题</span>
        <span className="rounded-full bg-card px-2 py-0.5 text-[10px] text-muted">{typeLabel}</span>
      </div>
      <div className="mt-1 text-sm text-txt">{q.stem}</div>
      {q.type !== 'fill' && (
        <ul className="mt-1 space-y-0.5 text-xs">
          {options.map((o, i) => (
            <li key={i} className={`inline-flex items-center gap-1 ${i === correctIndex ? 'text-emerald-600' : 'text-muted'}`}>
              {String.fromCharCode(65 + i)}. {o} {i === correctIndex && <Check size={12} />}
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
          <li key={s.step} className="rounded-lg border border-border bg-card-2 p-3 text-xs">
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
