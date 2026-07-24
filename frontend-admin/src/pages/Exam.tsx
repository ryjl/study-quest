import { useQuery } from '@tanstack/react-query';
import { ClipboardList, CheckCircle2, Gauge, CalendarPlus, AlertTriangle } from 'lucide-react';
import { api } from '../lib/api';
import { PageHeader } from '../components/PageHeader';
import { Section, StatCard, Spinner } from '../components/ui';

// Exam.tsx — 课程考试观测页 (TODO.md P0)。展示考试全局统计 + 题源质量对比
// (pool 题库抽 vs generated agent 新出)。每个聚合服务端独立降级,单点失败该区为
// 0/空,不拖垮整页。
//
// 数据源:GET /admin/api/exam/stats。低频访问(观测页),不做轮询。
// 视觉:复用 WrongBook.tsx 的 StatCard + Section + Tailwind CSS 横条范式(不引 recharts)。

// SOURCE_LABEL 把后端 source key 转成中文展示。
const SOURCE_LABEL: Record<string, string> = {
  pool: '题库题',
  generated: '新生成题',
};

export function Exam() {
  const statsQ = useQuery({
    queryKey: ['exam-stats'],
    queryFn: api.examStats,
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
        加载考试数据失败
      </div>
    );
  }
  const s = statsQ.data;
  if (!s) {
    return null; // isLoading 已在上面处理;此处兜底(理论上到不了)
  }

  // 题源质量横条:按固定顺序 pool → generated 展示(缺失的补 0,避免空段)。
  const order = ['pool', 'generated'];
  const rows = order
    .map((src) => s.source_quality.find((r) => r.source === src) ?? { source: src, total: 0, correct: 0, rate: 0 });

  return (
    <div>
      <PageHeader
        title="课程考试"
        description="学生阶段性综合测评;题源质量对比帮你判断迁移题难度是否合理。"
      />
      <div className="mt-6 space-y-6">
        {/* StatCard 顶栏 */}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard label="考试卷总数" value={s.total} icon={<ClipboardList size={16} />} color="#6366f1" />
          <StatCard
            label="已交卷"
            value={s.submitted}
            hint={s.total > 0 ? `占 ${Math.round((s.submitted / s.total) * 100)}%` : undefined}
            icon={<CheckCircle2 size={16} />}
            color="#10b981"
          />
          <StatCard
            label="平均得分率"
            value={s.submitted > 0 ? `${Math.round(s.avg_score * 100)}%` : '—'}
            icon={<Gauge size={16} />}
            color="#f59e0b"
          />
          <StatCard label="本周新开考" value={s.this_week} icon={<CalendarPlus size={16} />} color="#3b82f6" />
        </div>

        {/* 题源质量对比(pool vs generated 正确率) */}
        <Section
          title="题源质量对比"
          description="题库题 vs 新生成题的正确率——新题明显低于题库题说明难度过高或出题质量有问题。"
        >
          {s.source_quality.length === 0 && s.submitted === 0 ? (
            <p className="text-sm text-muted">暂无数据</p>
          ) : (
            <div className="space-y-4">
              {rows.map((r, i) => {
                const label = SOURCE_LABEL[r.source] ?? r.source;
                const colors = ['bg-indigo-500', 'bg-violet-500'];
                return (
                  <div key={r.source}>
                    <div className="mb-1 flex items-center justify-between gap-3 text-sm">
                      <span className="text-txt">{label}</span>
                      <span className="tabular-nums text-muted">
                        {r.correct}/{r.total} · {Math.round(r.rate * 100)}%
                      </span>
                    </div>
                    <div className="h-2 overflow-hidden rounded-full bg-card-2">
                      <div
                        className={`h-full rounded-full ${colors[i % colors.length]} transition-all`}
                        style={{ width: `${Math.round(r.rate * 100)}%` }}
                      />
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </Section>
      </div>
    </div>
  );
}
