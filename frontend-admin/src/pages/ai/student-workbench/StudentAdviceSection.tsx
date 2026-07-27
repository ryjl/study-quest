// StudentAdviceSection — 学生工作台「题库与建议」tab 的建议区。修复旧 UserAdviceCard
// 的核心缺陷:只让 admin "选目标 + 重生成/删除",却完全不显示当前已生成的建议文本——
// admin 根本不知道现在有没有建议、建议说了啥。
//
// 这里:查 listUserAdvice 拿当前 3 个 scope 的建议 → 按 scope 分组显示 advice_text +
// 生成时间/模型 + 重生成/删除。admin 先看到现状,再决定是否操作。
//
// 没有 advice 的 scope:显示"未生成"+ 一个"生成"按钮(选具体目标后触发)。
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../../lib/api';
import { useConfirm, useToast } from '../../../lib/toast';
import { MiniMarkdown } from '../../../components/ai/MiniMarkdown';
import { useTypedMutation } from '../../../lib/useTypedMutation';
import type { StudyAdviceRow } from '../../../lib/types';

const SCOPE_LABEL: Record<string, string> = {
  episode: '课时建议',
  course: '课程建议',
  subject: '学科建议',
};

// courseFilter(可选):传入时只显示该课程相关的建议。
//   - course scope:scope_id === courseFilter 直接匹配
//   - episode scope:scope_id 是 episode_id,用 episodeCourseMap 查它属于哪门课再比
//   - subject scope:学科级跨课程,选了具体课程时不算该课上下文,过滤掉
// courseFilter=null 显示全部。
// episodeTitleFor:episode_id → 课时标题。episode 建议卡片要显示"哪节课的建议",
// 否则一堆"课时建议"分不清。父组件已拉 episodes 数据,不额外发请求。
export function StudentAdviceSection({ userId, courseFilter, courseTitleFor, episodeCourseMap, episodeTitleFor }: {
  userId: number;
  courseFilter?: number | null;
  courseTitleFor?: (cid: number) => string;
  episodeCourseMap?: Map<number, number>;
  episodeTitleFor?: (eid: number) => string | undefined;
}) {
  const toast = useToast();
  const confirm = useConfirm();
  const qc = useQueryClient();
  const adviceQ = useQuery({
    queryKey: ['ai-user-advice', userId],
    queryFn: () => api.listUserAdvice(userId),
  });
  // 课程名解析:course scope 的 scope_id 是 course_id,join 出 title 显示。
  // courseTitleFor(父组件传)优先用,否则本组件用 courses 缓存兜底。
  const courseTitleById = useQuery({
    queryKey: ['courses'],
    queryFn: api.listCourses,
    enabled: !courseTitleFor, // 父组件已提供就不重复查
  });
  const resolveCourseTitle = (cid: number) =>
    courseTitleFor?.(cid) ?? (courseTitleById.data ?? []).find((c) => c.id === cid)?.title ?? `课程 ${cid}`;

  // courseFilter 过滤:每次 render 直接算(不用 useMemo,避免缓存导致切课程后旧建议残留)。
  //   - course scope:scope_id === courseFilter
  //   - episode scope:用 episodeCourseMap 查 episode→course,匹配 courseFilter
  //   - subject scope:学科级跨课程,选具体课程时过滤掉
  const allAdvices: StudyAdviceRow[] = adviceQ.data ?? [];
  const advices =
    courseFilter == null
      ? allAdvices
      : allAdvices.filter((a) => {
          const scope = String(a.scope).trim().toLowerCase();
          if (scope === 'course') return a.scope_id === courseFilter;
          if (scope === 'episode') {
            // episode 的 scope_id 是 episode_id,映射到 course_id 再比。
            const cid = episodeCourseMap?.get(a.scope_id);
            return cid === courseFilter;
          }
          // subject 是学科级跨课程,选了具体课程时不属于该课上下文。
          return false;
        });

  const regenMut = useMutation({
    mutationFn: (args: { scope: 'episode' | 'course' | 'subject'; scopeId: number }) =>
      api.regenerateUserAdvice(userId, args.scope, args.scopeId),
    onSuccess: (_d, vars) => {
      toast.success(`已入队 ${SCOPE_LABEL[vars.scope]},进度见「任务队列」`);
      qc.invalidateQueries({ queryKey: ['ai-user-advice', userId] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '入队失败'),
  });
  const delMut = useTypedMutation({
    mutationFn: (args: { scope: 'episode' | 'course' | 'subject'; scopeId: number }) =>
      api.deleteUserAdvice(userId, args.scope, args.scopeId),
    successMsg: '建议已删除',
    invalidateKeys: [['ai-user-advice', userId]],
    errorMsg: '删除失败',
  });

  const onDel = async (scope: 'episode' | 'course' | 'subject', scopeId: number) => {
    const ok = await confirm({
      message: `删除该学生的 ${SCOPE_LABEL[scope]}?`,
      detail: '删除后可重新生成。',
      danger: true,
    });
    if (ok) delMut.mutate({ scope, scopeId });
  };

  if (adviceQ.isLoading) {
    return <div className="rounded-lg border border-border bg-card p-4 text-sm text-muted">加载建议…</div>;
  }
  if (adviceQ.error) {
    return <div className="rounded-lg border border-border bg-card p-4 text-sm text-bad">加载失败:{(adviceQ.error as Error).message}</div>;
  }

  return (
    <section className="space-y-3 rounded-lg border border-border bg-card p-4">
      <header className="space-y-0.5">
        <h2 className="text-base font-semibold">学习建议</h2>
        <p className="text-xs text-muted">
          agent 按课时/课程/学科三个粒度生成的建议(基于该生的掌握度)。重生成是异步入队,进度在「任务队列」。
        </p>
      </header>

      {advices.length === 0 ? (
        <div className="rounded-md border border-dashed border-border bg-card-2 px-4 py-6 text-center text-sm text-muted">
          {courseFilter != null
            ? `该课程暂无 course 级建议${allAdvices.length > 0 ? `(该学生共有 ${allAdvices.length} 条其它建议,切回"全部课程"查看)` : ''}。`
            : '该学生暂无任何建议。可在课程工作台触发生成。'}
        </div>
      ) : (
        <ul className="space-y-2">
          {advices.map((a, i) => (
            <li key={`${a.scope}-${a.scope_id}-${i}`} className="rounded-md border border-border/60 bg-card-2 p-3">
              <div className="mb-1.5 flex items-center justify-between gap-2">
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="shrink-0 rounded-full bg-primary/10 px-2 py-0.5 text-[11px] font-medium text-primary">
                    {SCOPE_LABEL[a.scope] ?? a.scope}
                  </span>
                  {/* 每条建议都标清归属,避免"一堆课时建议分不清是哪节":
                      - episode:课时标题 + 所属课程(用 episodeCourseMap 反查 course,再 join title)
                      - course:课程名(scope_id 即 course_id)
                      - subject:学科级,不标具体对象 */}
                  {a.scope === 'episode' && (
                    <span className="truncate text-[11px] text-muted">
                      {episodeTitleFor?.(a.scope_id) ?? `课时 #${a.scope_id}`}
                      {(() => {
                        const cid = episodeCourseMap?.get(a.scope_id);
                        return cid != null ? ` · ${resolveCourseTitle(cid)}` : '';
                      })()}
                    </span>
                  )}
                  {a.scope === 'course' && (
                    <span className="truncate text-[11px] text-muted">{resolveCourseTitle(a.scope_id)}</span>
                  )}
                </div>
                <div className="flex items-center gap-1">
                  <button
                    className="btn-ghost btn-sm"
                    onClick={() => regenMut.mutate({ scope: a.scope as 'episode' | 'course' | 'subject', scopeId: a.scope_id })}
                    disabled={regenMut.isPending}
                    title="重新生成该 scope 的建议(替换当前)"
                  >
                    {regenMut.isPending ? '提交中…' : '重生成'}
                  </button>
                  <button
                    className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                    onClick={() => onDel(a.scope as 'episode' | 'course' | 'subject', a.scope_id)}
                    disabled={delMut.isPending}
                  >
                    删除
                  </button>
                </div>
              </div>
              <MiniMarkdown text={a.advice_text} />
              <div className="mt-2 flex flex-wrap gap-x-3 gap-y-0.5 text-[10px] text-muted">
                <span>生成于 {new Date(a.generated_at).toLocaleString('zh-CN')}</span>
                {a.model_used && <span>模型: {a.model_used}</span>}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
