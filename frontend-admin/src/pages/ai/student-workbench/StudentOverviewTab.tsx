// StudentOverviewTab — 学生工作台「概览」tab。复用 UserStudyReportCard(学习报告,
// 三态:未生成/生成中/已生成),它是 per-user 的,直接传 userId。
import { UserStudyReportCard } from '../../ai-console/regen/UserStudyReportCard';

export function StudentOverviewTab({ userId }: { userId: number }) {
  return (
    <div className="space-y-4">
      <UserStudyReportCard userId={userId} />
    </div>
  );
}
