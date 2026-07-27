// StudentWorkbench — 学生级 AI 工作台。"对象即导航"重构的第二个载体:
// 围绕「一个学生」把他的所有 AI 学习数据聚合到一个页面。
//
// 4 个 tab:
//   - 概览:跨课程学习报告 + 该生待办(失败 quiz 等)
//   - 题库:该生的 quiz 列表 + 详情(答题历史/掌握度/agent 评价/思考时间线)
//   - 错题:全局错题观测(该生视角)+ 重出题入口
//   - 操作:重新生成 学习报告/建议/quiz(原 UserRegenColumn 的三个卡片)
//
// 修复的断裂:旧版"看学生答错(学生数据 tab)→ 重出题(内容管理→按学生)"跨 tab,
// 现在题库/错题 和 操作 同在一个工作台。

import { useSearchParams, useParams, Navigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { api } from '../../lib/api';
import { PageHeader } from '../../components/PageHeader';
import { ROLES } from '../users/Users';
import { StudentOverviewTab } from './student-workbench/StudentOverviewTab';
import { StudentContentTab } from './student-workbench/StudentContentTab';
import { StudentWrongTab } from './student-workbench/StudentWrongTab';

// 3 tab:概览(学习报告) / 题库与建议(看数据+操作) / 错题。
// 旧版「操作」tab(只操作不展示数据)和「题库」tab 合并进「题库与建议」——
// 一个 tab 同时展示当前数据(题库列表可展开看题、各 scope 建议文本)和操作
// (重生成/删除),不再"看一处改另一处"。
const TABS = [
  { key: 'overview', label: '概览', hint: '跨课程学习报告 + 待办' },
  { key: 'content', label: '题库与建议', hint: '该生的题库(可展开看题)、答题历史、学习建议,带重生成/删除' },
  { key: 'wrong', label: '错题', hint: '该生做错的题,带重出题入口' },
] as const;

type TabKey = (typeof TABS)[number]['key'];
const DEFAULT_TAB: TabKey = 'overview';

function isTabKey(s: string | null): s is TabKey {
  return !!s && (TABS as readonly { key: string }[]).some((t) => t.key === s);
}

export function StudentWorkbench() {
  const { userId: userIdStr } = useParams<{ userId: string }>();
  const [params, setParams] = useSearchParams();
  const userId = Number(userIdStr);
  const validUserId = Number.isFinite(userId) && userId > 0 ? userId : null;

  const rawTab = params.get('tab');
  const tab: TabKey = isTabKey(rawTab) ? rawTab : DEFAULT_TAB;
  const setTab = (t: string) => {
    const next = new URLSearchParams(params);
    next.set('tab', t);
    setParams(next, { replace: true });
  };

  // 学生基本信息——给 PageHeader 显示昵称。
  const usersQ = useQuery({ queryKey: ['users'], queryFn: api.listUsers });
  const user = useMemo(
    () => (usersQ.data ?? []).find((u) => u.id === validUserId) ?? null,
    [usersQ.data, validUserId],
  );

  if (validUserId == null) {
    return <Navigate to="/admin/ai/students" replace />;
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={user ? user.nickname : `学生 #${validUserId}`}
        breadcrumb={[
          { label: 'AI 运营' },
          { label: '学生工作台', to: '/admin/ai/students' },
        ]}
        description={user?.role ? `角色:${ROLES.find((r) => r.key === user.role)?.label ?? user.role} · 围绕这个学生集中管理 AI 学习数据` : '围绕这个学生集中管理 AI 学习数据'}
      />

      <div className="flex flex-wrap gap-1.5 border-b border-border">
        {TABS.map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            title={t.hint}
            className={`rounded-t-md px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key ? 'border-b-2 border-primary text-primary' : 'text-muted hover:text-txt'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div>
        {tab === 'overview' && <StudentOverviewTab userId={validUserId} />}
        {tab === 'content' && <StudentContentTab userId={validUserId} />}
        {tab === 'wrong' && <StudentWrongTab userId={validUserId} />}
      </div>
    </div>
  );
}
