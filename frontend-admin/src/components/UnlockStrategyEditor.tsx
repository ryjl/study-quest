import { X, Plus } from 'lucide-react';
import { STRATEGIES, WEEKDAY_LABELS } from '../lib/useUnlock';
import type { UnlockStrategy, WeeklyTime } from '../lib/types';

// Shared, controlled editor for one unlock strategy config. Used by both the
// course-level template page and the per-user override modal so the two never
// drift in shape. All state is held by the parent; this is a pure form.

export interface StrategyValue {
  strategy: UnlockStrategy;
  interval_seconds: number;
  weekly_times: WeeklyTime[];
}

export function UnlockStrategyEditor({
  value,
  onChange,
}: {
  value: StrategyValue;
  onChange: (v: StrategyValue) => void;
}) {
  const set = (patch: Partial<StrategyValue>) => onChange({ ...value, ...patch });
  const meta = STRATEGIES.find((s) => s.key === value.strategy);

  return (
    <div className="space-y-3">
      <div>
        <label className="mb-1 block text-xs text-muted">解锁策略</label>
        <select
          className="input"
          value={value.strategy}
          onChange={(e) => {
            const next = e.target.value as UnlockStrategy;
            // Reset strategy-specific defaults on switch so a stale interval/
            // weekly value from a previous mode doesn't leak through.
            const patch: Partial<StrategyValue> = { strategy: next };
            if (next === 'interval') patch.interval_seconds = value.interval_seconds || 86400;
            if (next === 'weekly' && value.weekly_times.length === 0) {
              patch.weekly_times = [{ weekday: 0, hour: 19, minute: 0 }];
            }
            set(patch);
          }}
        >
          {STRATEGIES.map((s) => (
            <option key={s.key} value={s.key}>
              {s.label}
            </option>
          ))}
        </select>
        {meta && <p className="mt-1 text-[11px] text-muted">{meta.hint}</p>}
      </div>

      {value.strategy === 'interval' && (
        <IntervalEditor seconds={value.interval_seconds} onChange={(s) => set({ interval_seconds: s })} />
      )}

      {value.strategy === 'weekly' && (
        <WeeklyTimesEditor times={value.weekly_times} onChange={(wt) => set({ weekly_times: wt })} />
      )}

      {value.strategy === 'selected' && (
        <p className="rounded-lg bg-card-2 px-3 py-2 text-[11px] text-muted">
          该策略下课时可见性完全来自 admin 勾选的「允许名单」，可在用户授权抽屉里为每个学生勾选具体课时（支持跳选）。
        </p>
      )}
    </div>
  );
}

function IntervalEditor({ seconds, onChange }: { seconds: number; onChange: (s: number) => void }) {
  // Present as value + unit so admins think "每 3 天" not "259200 秒".
  const days = Math.floor(seconds / 86400);
  const remHours = Math.floor((seconds % 86400) / 3600);
  return (
    <div className="flex items-end gap-2">
      <div>
        <label className="mb-1 block text-xs text-muted">间隔</label>
        <input
          type="number"
          min={1}
          className="input w-24"
          value={days}
          onChange={(e) => onChange(Math.max(0, Number(e.target.value)) * 86400 + remHours * 3600)}
        />
      </div>
      <span className="pb-2 text-xs text-muted">天</span>
      <div>
        <select
          className="input w-24"
          value={remHours}
          onChange={(e) => onChange(days * 86400 + Number(e.target.value) * 3600)}
        >
          <option value={0}>+0 时</option>
          <option value={6}>+6 时</option>
          <option value={12}>+12 时</option>
          <option value={18}>+18 时</option>
        </select>
      </div>
    </div>
  );
}

function WeeklyTimesEditor({ times, onChange }: { times: WeeklyTime[]; onChange: (t: WeeklyTime[]) => void }) {
  const update = (i: number, patch: Partial<WeeklyTime>) => {
    const next = times.map((t, idx) => (idx === i ? { ...t, ...patch } : t));
    onChange(next);
  };
  const remove = (i: number) => onChange(times.filter((_, idx) => idx !== i));
  const add = () => onChange([...times, { weekday: 0, hour: 19, minute: 0 }]);

  return (
    <div className="space-y-2">
      <label className="block text-xs text-muted">每周解锁时间点（业务时区）</label>
      {times.map((t, i) => (
        <div key={i} className="flex items-center gap-2">
          <select className="input w-24" value={t.weekday} onChange={(e) => update(i, { weekday: Number(e.target.value) })}>
            {WEEKDAY_LABELS.map((lbl, idx) => (
              <option key={idx} value={idx}>
                {lbl}
              </option>
            ))}
          </select>
          <select
            className="input w-20"
            value={t.hour}
            onChange={(e) => update(i, { hour: Number(e.target.value) })}
          >
            {Array.from({ length: 24 }, (_, h) => (
              <option key={h} value={h}>
                {String(h).padStart(2, '0')}
              </option>
            ))}
          </select>
          <span className="text-xs text-muted">:</span>
          <select
            className="input w-20"
            value={t.minute}
            onChange={(e) => update(i, { minute: Number(e.target.value) })}
          >
            {[0, 15, 30, 45].map((m) => (
              <option key={m} value={m}>
                {String(m).padStart(2, '0')}
              </option>
            ))}
          </select>
          <button type="button" className="btn-ghost btn-sm" onClick={() => remove(i)}>
            <X size={12} />
          </button>
        </div>
      ))}
      <button type="button" className="btn-secondary btn-sm inline-flex items-center gap-1" onClick={add}>
        <Plus size={14} /> 添加时间点
      </button>
    </div>
  );
}
