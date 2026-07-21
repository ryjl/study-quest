// ScopeRow — one row in the 学习建议 card. A label, a freeform select slot
// (the caller renders the appropriate dropdowns for episode / course / subject
// scope), and the regen / delete buttons. Buttons are gated by `canAct` so a
// row with no target selected can't fire.

export function ScopeRow({
  title,
  select,
  onRegen,
  onDel,
  canAct,
  regenPending,
  delPending,
}: {
  title: string;
  select: React.ReactNode;
  onRegen: () => void;
  onDel: () => void;
  canAct: boolean;
  regenPending: boolean;
  delPending: boolean;
}) {
  return (
    <div className="rounded-md border border-border/60 bg-card px-2.5 py-1.5">
      <div className="mb-1 text-[11px] font-medium text-muted">{title}</div>
      <div className="flex flex-wrap items-center justify-between gap-1.5">
        <div className="min-w-0 flex-1">{select}</div>
        <div className="flex flex-shrink-0 items-center gap-1">
          <button
            className="btn-ghost btn-sm"
            onClick={onRegen}
            disabled={!canAct || regenPending}
            title="重新生成(异步入队)"
          >
            {regenPending ? '提交中…' : '重新生成'}
          </button>
          <button
            className="btn-ghost btn-sm text-bad hover:bg-bad/10"
            onClick={onDel}
            disabled={!canAct || delPending}
            title="删除现有建议"
          >
            {delPending ? '删除中…' : '删除'}
          </button>
        </div>
      </div>
    </div>
  );
}

// scopeLabel — the Chinese name for an advice scope. Used in toast + confirm
// messages ("已入队 课程建议" / "删除该学生的 学科建议?").
export function scopeLabel(scope: 'episode' | 'course' | 'subject'): string {
  return scope === 'episode' ? '课时' : scope === 'course' ? '课程' : '学科';
}
