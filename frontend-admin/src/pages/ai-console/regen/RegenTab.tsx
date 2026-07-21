// RegenTab — the "重新生成" tab on the AI Console. Two columns:
//   - 按课程操作 (left): pick a course → trigger/delete course summary,
//     list episodes with per-episode summary regen/delete.
//   - 按学生操作 (right): pick a user → 3-state study report, 3-scope advice
//     (episode/course/subject), and the user's quiz list with regen/delete
//     per row.
//
// AI附加层原则: if no chat provider is configured (the global "AI is on"
// signal), the WHOLE tab shows a placeholder pointing to the Provider tab.
// AI is an opt-in add-on — without a chat backend, none of these actions
// would produce anything.

import { useMemo } from 'react';
import { useAiProviders } from '../../../lib/useAiProviders';
import { CourseRegenColumn } from './CourseRegenColumn';
import { UserRegenColumn } from './UserRegenColumn';

export function RegenTab() {
  const providersQ = useAiProviders();
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
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <CourseRegenColumn />
      <UserRegenColumn />
    </div>
  );
}
