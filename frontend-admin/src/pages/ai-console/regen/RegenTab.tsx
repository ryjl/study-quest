// RegenTab — the "重新生成" tab on the AI Console.
//
// v2 改造(2026-07-26):从"两列并排(grid-cols-2)"改成"两个子 tab 切换"。
// 用户反馈:课程操作和学生操作是两个很独立的功能,并排占用空间,每行课程/学生
// 显示空间被压缩。改成 tab 后,每个 tab 独占全宽,行内容更宽松,也能容纳缩略图等
// 更多信息。
//
// 两个子 tab:
//   - 按课程操作 (CourseRegenColumn):课程总结、课时总结、作业生成/预览。
//   - 按学生操作 (UserRegenColumn):学习报告、建议、quiz 重做。
//
// AI附加层原则:if no chat provider is configured (the global "AI is on"
// signal), the WHOLE tab shows a placeholder pointing to the Provider tab。

import { useMemo, useState } from 'react';
import { Tabs, type TabItem } from '../../../components/ui';
import { useAiProviders } from '../../../lib/useAiProviders';
import { CourseRegenColumn } from './CourseRegenColumn';
import { UserRegenColumn } from './UserRegenColumn';

const REGEN_TABS: TabItem[] = [
  { key: 'course', label: '按课程' },
  { key: 'user', label: '按学生' },
];

export function RegenTab() {
  const providersQ = useAiProviders();
  const [tab, setTab] = useState('course');
  // "Configured" = at least one enabled chat provider. This is the same
  // signal AiProvidersSection acts on (it only manages chat). Embedding
  // models are auto-seeded and never user-toggleable, so chat is the gate.
  const configured = useMemo(() => {
    const list = providersQ.data ?? [];
    return list.some((p) => p.capability === 'chat' && p.is_enabled);
  }, [providersQ.data]);

  if (providersQ.isLoading) {
    return (
      <div className="rounded-lg border border-border bg-card px-4 py-10 text-center text-sm text-muted">
        加载中…
      </div>
    );
  }
  if (!configured) {
    return (
      <div className="rounded-lg border border-dashed border-warn/40 bg-warn/5 px-4 py-10 text-center">
        <p className="text-sm font-medium text-warn">AI 未配置</p>
        <p className="mt-1 text-xs text-muted">
          请到「Provider」标签配置聊天模型后重试。AI 是附加层,未配置时无法重新生成内容。
        </p>
      </div>
    );
  }
  return (
    <div className="space-y-4">
      <Tabs tabs={REGEN_TABS} value={tab} onChange={setTab} />
      {/* v2:两个 panel 都常驻 DOM,用 hidden 类切显隐而非条件渲染(unmount)。
          原来用 {tab==='course' && ...} 切 tab 会卸载组件,丢失内部 useState
          (CourseRegenColumn 勾选的课时 selected、UserRegenColumn 的草稿等)。
          keep-alive:切换不丢状态。React Query 数据有缓存本来不闪,useState 才是关键。 */}
      <div className={tab === 'course' ? '' : 'hidden'}>
        <CourseRegenColumn />
      </div>
      <div className={tab === 'user' ? '' : 'hidden'}>
        <UserRegenColumn />
      </div>
    </div>
  );
}
