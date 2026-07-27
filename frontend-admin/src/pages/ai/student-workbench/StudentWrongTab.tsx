// StudentWrongTab — 学生工作台「错题」tab。
//
// 诚实的设计:错题本统计端点(/admin/api/wrong-book/stats)是全局聚合,不支持按
// user 过滤(零后端改动原则下不改它)。所以这里:
//   1. 顶部一个提示框,说明"要按这个学生看具体错题 → 去题库 tab 逐个 quiz 看答题历史"
//   2. 下方复用全局错题本观测(WrongBook embedded),作为"共性题面问题"的参考
//      (高频错题榜 = 全员都错,可能是题面/知识点问题,admin 调优时参考)
//
// 这样不假装能过滤(避免误导),又保留了错题观测的价值(发现共性质量问题)。
import { useSearchParams } from 'react-router-dom';
import { WrongBook } from '../../WrongBook';
import { Info } from 'lucide-react';

export function StudentWrongTab({ userId: _userId }: { userId: number }) {
  const [, setParams] = useSearchParams();
  return (
    <div className="space-y-4">
      <div className="flex items-start gap-2.5 rounded-lg border border-blue-500/30 bg-blue-500/5 p-3">
        <Info size={15} className="mt-0.5 shrink-0 text-blue-600" />
        <div className="text-xs text-txt">
          <p className="font-medium">想看这个学生的具体错题?</p>
          <p className="mt-0.5 text-muted">
            错题统计目前是全局聚合。该生每道题的对错记录在{' '}
            <button
              className="text-primary underline-offset-2 hover:underline"
              onClick={() => setParams({ tab: 'quizzes' }, { replace: true })}
            >
              题库 tab
            </button>{' '}
            里逐个 quiz 的"答题历史"中。下方是全局错题观测,帮你发现全员共性题面问题。
          </p>
        </div>
      </div>
      <WrongBook embedded />
    </div>
  );
}
