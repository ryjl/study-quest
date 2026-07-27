import { useQuery } from '@tanstack/react-query';
import { BookX, CheckCircle2, TrendingUp, CalendarPlus, AlertTriangle } from 'lucide-react';
import { api } from '../lib/api';
import { PageHeader } from '../components/PageHeader';
import { Section, StatCard, Spinner } from '../components/ui';

// WrongBook.tsx — 错题本观测页。展示错题本全局统计 + 高频错题榜 + 科目弱点分布。
// 每个聚合服务端独立降级,单点失败该区为 0/空,不拖垮整页。
//
// 数据源:GET /admin/api/wrong-book/stats。低频访问(观测页),不做轮询。
// 视觉:复用 Dashboard 的 StatCard + Section + Tailwind CSS 横条(全仓约定,不引 recharts)。
//
// embedded=true 时跳过 PageHeader,由外层(AI 控制台观测组)提供标题——和
// AIWorkflow/AIUserView 的 embedded 约定一致。

export function WrongBook({ embedded = false }: { embedded?: boolean }) {
  const statsQ = useQuery({
    queryKey: ['wrong-book-stats'],
    queryFn: api.wrongBookStats,
  });

  if (statsQ.isLoading) {
    return (
      <div className="flex items-center justify-center py-24">
        <Spinner size={28} />
      </div>
    );
  }
  if (statsQ.isError) {
    return (
      <div className="flex items-center justify-center py-24 text-sm text-muted">
        <AlertTriangle size={16} className="mr-2" />
        加载错题本数据失败
      </div>
    );
  }
  const s = statsQ.data;
  if (!s) {
    return null; // isLoading 已在上面处理;此处兜底(理论上到不了)
  }
  const maxSubject = Math.max(...s.by_subject.map((x) => x.count), 1);
  const maxFrequent = Math.max(...s.top_frequent.map((x) => x.occur_count), 1);

  return (
    <div>
      {!embedded && (
        <PageHeader
          title="错题本"
          description="学生做错的题自动归集;高频错题和科目弱点分布帮你发现题面问题或难点。"
        />
      )}
      <div className={embedded ? 'space-y-6' : 'mt-6 space-y-6'}>
        {/* StatCard 顶栏 */}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard label="错题总数" value={s.total} icon={<BookX size={16} />} color="#6366f1" />
          <StatCard
            label="未掌握"
            value={s.unmastered}
            hint={s.total > 0 ? `占 ${Math.round((s.unmastered / s.total) * 100)}%` : undefined}
            icon={<AlertTriangle size={16} />}
            color="#f59e0b"
          />
          <StatCard
            label="已掌握转化率"
            value={`${Math.round(s.master_rate * 100)}%`}
            icon={<CheckCircle2 size={16} />}
            color="#10b981"
          />
          <StatCard label="本周新增" value={s.this_week} icon={<CalendarPlus size={16} />} color="#3b82f6" />
        </div>

        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          {/* 高频错题榜 */}
          <Section title="高频错题榜" icon={<TrendingUp size={16} />} description="错的学生最多的题——全员都错可能是题面有问题或知识点太难。">
            {s.top_frequent.length === 0 ? (
              <p className="text-sm text-muted">暂无数据</p>
            ) : (
              <div className="space-y-3">
                {s.top_frequent.map((q) => (
                  <div key={q.question_id}>
                    <div className="mb-1 flex items-start justify-between gap-3 text-sm">
                      <span className="line-clamp-2 text-txt" title={q.stem}>{q.stem}</span>
                      <span className="shrink-0 tabular-nums text-muted">{q.occur_count} 人错</span>
                    </div>
                    <div className="h-2 overflow-hidden rounded-full bg-card-2">
                      <div
                        className="h-full rounded-full bg-indigo-500 transition-all"
                        style={{ width: `${(q.occur_count / maxFrequent) * 100}%` }}
                      />
                    </div>
                    <div className="mt-0.5 text-xs text-muted">累计重做 {q.total_attempts} 次</div>
                  </div>
                ))}
              </div>
            )}
          </Section>

          {/* 科目弱点分布 */}
          <Section title="科目弱点分布" icon={<BookX size={16} />} description="各科目错题量——哪一科错得最多。">
            {s.by_subject.length === 0 ? (
              <p className="text-sm text-muted">暂无数据</p>
            ) : (
              <div className="space-y-2">
                {s.by_subject.map((sub) => (
                  <div key={sub.subject_key}>
                    <div className="mb-1 flex justify-between text-sm">
                      <span className="text-txt">{sub.subject_label}</span>
                      <span className="tabular-nums text-muted">{sub.count} 题</span>
                    </div>
                    <div className="h-2 overflow-hidden rounded-full bg-card-2">
                      <div
                        className="h-full rounded-full bg-violet-500 transition-all"
                        style={{ width: `${(sub.count / maxSubject) * 100}%` }}
                      />
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Section>
        </div>
      </div>
    </div>
  );
}
