// CourseContentTab — 课程工作台「内容」tab。直接复用 CourseRegenColumn,传入
// courseId 让它进入"课程已固定"模式(隐藏选课程下拉)。
//
// AI 未配置 gate:AI 是附加层,没配 chat provider 时整个内容 tab 显示占位,引导去
// 系统设置配置(Provider 已从 AI 控制台挪到系统设置)。gate 逻辑从旧 RegenTab 搬来。
import { useMemo } from 'react';
import { useAiProviders } from '../../../lib/useAiProviders';
import { CourseRegenColumn } from '../../ai-console/regen/CourseRegenColumn';

export function CourseContentTab({ courseId }: { courseId: number }) {
  const providersQ = useAiProviders();
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
          请到「系统设置 → AI Provider 配置」配置聊天模型后重试。AI 是附加层,未配置时无法生成内容。
        </p>
      </div>
    );
  }
  return <CourseRegenColumn key={courseId} courseId={courseId} />;
}
