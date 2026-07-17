import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api } from '../lib/api';
import { useBadges, useInvalidateBadges } from '../lib/useBadges';
import { useDeleteConfirm } from '../lib/useDeleteConfirm';
import type { AdminBadge } from '../lib/types';
import { useSubjects } from '../lib/useSubjects';
import { Modal, LoadingState, EmptyState } from '../components/ui';
import { useToast } from '../lib/toast';
import { PageHeader } from '../components/PageHeader';

const ICONS = [
  { key: 'badge_first_blood', emoji: '✨', label: '首战告捷' },
  { key: 'badge_streak_7', emoji: '🔥', label: '七日先锋' },
  { key: 'badge_math', emoji: '🧮', label: '数学达人' },
  { key: 'badge_english', emoji: '🗣️', label: '英语之星' },
  { key: 'badge_gold', emoji: '🏆', label: '黄金大满贯' },
];

const RULE_TYPES = [
  { key: 'watch_duration', label: '累计学习时长（分钟）' },
  { key: 'consecutive_days', label: '连续活跃天数（天）' },
  { key: 'subject_count', label: '特定科目完课数（课时）' },
  { key: 'episode_completed_count', label: '累计完成课时数（课时）' },
  { key: 'distinct_subject_count', label: '完成不同科目数（个）' },
  { key: 'course_completion', label: '完整通关课程数（门）' },
  { key: 'weekly_all_present', label: '近7天活跃天数（天）' },
  { key: 'points_earned', label: '累计获得星币' },
];

// Rule types that need a subject target (the rule_target field).
const RULE_TYPES_WITH_SUBJECT = new Set(['subject_count']);

// Leaf rule shape — mirrors the backend model.CompositeRule leaf node.
interface LeafRule {
  type: string;
  target: string;
  threshold: number;
}
// Composite rule tree — mirrors model.CompositeRule. A group has logic + rules;
// a leaf has type/target/threshold. We represent both with one shape and
// distinguish by presence of `rules`.
interface RuleNode {
  logic?: 'and' | 'or';
  rules?: RuleNode[];
  type?: string;
  target?: string;
  threshold?: number;
}

function emojiFor(iconName: string | undefined) {
  if (!iconName) return '🏅';
  return ICONS.find((i) => iconName.includes(i.key.split('_')[1]))?.emoji ?? '🏅';
}

// ruleLabel maps a rule type key to a short Chinese label for display.
function ruleLabel(type?: string): string {
  return RULE_TYPES.find((r) => r.key === type)?.label ?? type ?? '?';
}

// ruleSummary renders a one-line human description of a badge's rule(s):
// single rule → "类型 ≥ N"; composite → "(条件1 且/或 条件2)".
function ruleSummary(b: AdminBadge): string {
  if (b.RuleType === 'composite' && b.RuleJSON) {
    try {
      const tree = JSON.parse(b.RuleJSON) as RuleNode;
      return compositeSummary(tree);
    } catch {
      return '组合规则（解析失败）';
    }
  }
  const t = ruleLabel(b.RuleType);
  if (RULE_TYPES_WITH_SUBJECT.has(b.RuleType) && b.RuleTarget) {
    return `${t} [${b.RuleTarget}] ≥ ${b.Threshold}`;
  }
  return `${t} ≥ ${b.Threshold}`;
}

function compositeSummary(node: RuleNode): string {
  if (node.rules && node.rules.length > 0) {
    const op = node.logic === 'or' ? ' 或 ' : ' 且 ';
    const parts = node.rules.map(compositeSummary);
    return `(${parts.join(op)})`;
  }
  const t = node.type ?? '';
  if (t && RULE_TYPES_WITH_SUBJECT.has(t) && node.target) {
    return `${ruleLabel(t)}[${node.target}]≥${node.threshold ?? 0}`;
  }
  return `${ruleLabel(t)}≥${node.threshold ?? 0}`;
}

export function Badges() {
  const [editing, setEditing] = useState<AdminBadge | null>(null);
  const [creating, setCreating] = useState(false);

  const badgesQ = useBadges();
  const invalidateBadges = useInvalidateBadges();
  const badges = badgesQ.data ?? [];

  const del = useDeleteConfirm({
    mutationFn: api.deleteBadge,
    noun: '徽章',
    onDeleted: invalidateBadges,
  });

  return (
    <div>
      <PageHeader
        title="荣誉徽章"
        breadcrumb={[{ label: '系统配置' }]}
        description="管理成就徽章与解锁规则。"
        actions={
          <button className="btn-primary" onClick={() => setCreating(true)}>
            + 新增勋章
          </button>
        }
      />

      <div className="grid grid-cols-3 gap-5">
        <div className="col-span-2">
          {badgesQ.isLoading ? (
            <LoadingState />
          ) : badges.length === 0 ? (
            <EmptyState icon="🏅" title="暂无徽章" />
          ) : (
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              {badges.map((b) => (
                <div key={b.ID} className="card flex flex-col items-center text-center">
                  <div className="mb-2 flex items-center gap-1.5 text-4xl">
                    {emojiFor(b.IconName)}
                    {b.IsSystem && (
                      <span className="text-xs" title="系统默认勋章，不可删除（可在编辑里修改）">🔒</span>
                    )}
                  </div>
                  <strong className="text-txt">{b.Title}</strong>
                  <span className="mb-3 mt-1 line-clamp-2 h-8 text-xs text-muted">{b.Description || '暂无说明'}</span>
                  <div className="mb-3 w-full rounded-md border border-border bg-card-2 px-2 py-1 text-[10px] text-primary">
                    {ruleSummary(b)}
                  </div>
                  <div className="flex w-full gap-1.5">
                    <button className="btn-secondary btn-sm flex-1" onClick={() => setEditing(b)}>
                      编辑
                    </button>
                    {b.IsSystem ? (
                      <button className="btn-danger btn-sm flex-1 opacity-40" disabled title="系统默认勋章，不可删除">
                        删除
                      </button>
                    ) : (
                      <button
                        className="btn-danger btn-sm flex-1"
                        onClick={() => del.confirmAndDelete(
                          b.ID,
                          `删除「${b.Title}」徽章？`,
                          '将清除所有学生的解锁状态。',
                        )}
                        disabled={del.isPending}
                      >
                        删除
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="card h-fit">
          <h2 className="mb-3 text-sm font-bold text-txt">规则引擎说明</h2>
          <ul className="space-y-2 text-xs text-muted">
            <li><strong className="text-primary">累计学习时长</strong> watch_duration（分钟）</li>
            <li><strong className="text-good">连续活跃天数</strong> consecutive_days（天）</li>
            <li><strong className="text-primary">特定科目完课数</strong> subject_count（需选科目）</li>
            <li><strong className="text-primary">累计完成课时数</strong> episode_completed_count（课时）</li>
            <li><strong className="text-primary">完成不同科目数</strong> distinct_subject_count（个）</li>
            <li><strong className="text-good">完整通关课程数</strong> course_completion（学完所有视频算1门）</li>
            <li><strong className="text-good">近7天活跃天数</strong> weekly_all_present（天，0-7）</li>
            <li><strong className="text-bad">累计星币</strong> points_earned</li>
            <li className="pt-2 text-primary">组合规则：可把多个条件用「全部满足(且)/任一满足(或)」组合，如「连续7天且累计60分钟」。</li>
          </ul>
        </div>
      </div>

      {(editing || creating) && (
        <BadgeModal
          badge={editing}
          onClose={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={() => {
            setEditing(null);
            setCreating(false);
            invalidateBadges();
          }}
        />
      )}
    </div>
  );
}

function BadgeModal({ badge, onClose, onSaved }: { badge: AdminBadge | null; onClose: () => void; onSaved: () => void }) {
  const isEdit = !!badge;
  const toast = useToast();
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];
  const [code, setCode] = useState('');
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [iconName, setIconName] = useState('badge_first_blood');
  // Single-rule fields.
  const [ruleType, setRuleType] = useState('watch_duration');
  const [ruleTarget, setRuleTarget] = useState('');
  const [threshold, setThreshold] = useState(1);
  // Multi-tier: when on, the badge stores a Tiers JSON array instead of a
  // single Threshold. Each tier = {threshold, reward}. Users add/remove rows.
  const [multiTier, setMultiTier] = useState(false);
  const [tierRows, setTierRows] = useState<{ t: number; r: number }[]>([
    { t: 3, r: 10 },
    { t: 7, r: 20 },
  ]);
  // 'single' | 'composite' — toggles which editor renders. A composite badge
  // serializes to rule_json + rule_type='composite'; single uses the legacy
  // rule_type/target/threshold fields.
  const [mode, setMode] = useState<'single' | 'composite'>('single');
  // Composite rule tree. Root is always a group with logic + rules[].
  const [tree, setTree] = useState<RuleNode>({ logic: 'and', rules: [{ type: 'watch_duration', target: '', threshold: 60 }] });

  useEffect(() => {
    if (badge) {
      setCode(badge.Code);
      setTitle(badge.Title);
      setDescription(badge.Description);
      setIconName(badge.IconName);
      if (badge.RuleType === 'composite' && badge.RuleJSON) {
        setMode('composite');
        try {
          const parsed = JSON.parse(badge.RuleJSON) as RuleNode;
          setTree(parsed && parsed.rules ? parsed : { logic: 'and', rules: [{ type: 'watch_duration', target: '', threshold: 60 }] });
        } catch {
          setTree({ logic: 'and', rules: [{ type: 'watch_duration', target: '', threshold: 60 }] });
        }
      } else {
        setMode('single');
        setRuleType(badge.RuleType || 'watch_duration');
        setRuleTarget(badge.RuleTarget);
        setThreshold(badge.Threshold || 1);
        // Load multi-tier Tiers if present.
        if (badge.Tiers) {
          try {
            const parsed = JSON.parse(badge.Tiers) as { t: number; r: number }[];
            if (Array.isArray(parsed) && parsed.length > 0) {
              setMultiTier(true);
              setTierRows(parsed);
            } else {
              setMultiTier(false);
              setTierRows([{ t: 3, r: 10 }, { t: 7, r: 20 }]);
            }
          } catch {
            setMultiTier(false);
            setTierRows([{ t: 3, r: 10 }, { t: 7, r: 20 }]);
          }
        } else {
          setMultiTier(false);
          setTierRows([{ t: 3, r: 10 }, { t: 7, r: 20 }]);
        }
      }
    }
  }, [badge]);

  const saveMut = useMutation({
    mutationFn: async () => {
      if (mode === 'composite') {
        // Validate: every leaf needs a type + threshold > 0.
        const leaves = collectLeaves(tree);
        if (leaves.length === 0) throw new Error('组合规则至少需要一个条件');
        for (const lf of leaves) {
          if (!lf.type) throw new Error('每个条件都需要选择规则类型');
          if (!lf.threshold || lf.threshold <= 0) throw new Error('每个条件的阈值需大于 0');
          if (RULE_TYPES_WITH_SUBJECT.has(lf.type) && !lf.target) throw new Error('subject_count 条件需要选择科目');
        }
        const body = {
          code, title, description, icon_name: iconName,
          rule_type: 'composite', rule_target: '', threshold: 0,
          rule_json: JSON.stringify(tree),
        };
        if (isEdit && badge) return api.updateBadge(badge.ID, body);
        return api.createBadge(body);
      }
      if (multiTier) {
        const valid = tierRows.filter((r) => r.t > 0);
        if (valid.length === 0) throw new Error('多层级至少需要一行有效阈值（>0）');
        const body = {
          code, title, description, icon_name: iconName,
          rule_type: ruleType, rule_target: ruleTarget,
          threshold: 0,
          tiers: JSON.stringify(valid),
        };
        if (isEdit && badge) return api.updateBadge(badge.ID, body);
        return api.createBadge(body);
      }
      const body = { code, title, description, icon_name: iconName, rule_type: ruleType, rule_target: ruleTarget, threshold, tiers: '' };
      if (isEdit && badge) return api.updateBadge(badge.ID, body);
      return api.createBadge(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '徽章已更新' : '徽章已创建');
      onSaved();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑徽章' : '创建徽章'} size="lg">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="mb-1 block text-xs text-muted">唯一标识码</label>
            <input className="input" value={code} onChange={(e) => setCode(e.target.value)} required disabled={isEdit} placeholder="math_expert" />
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">勋章名称</label>
            <input className="input" value={title} onChange={(e) => setTitle(e.target.value)} required />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">描述</label>
          <input className="input" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="累计学完5个数学课时" />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">图标</label>
          <select className="input" value={iconName} onChange={(e) => setIconName(e.target.value)}>
            {ICONS.map((i) => (
              <option key={i.key} value={i.key}>
                {i.emoji} {i.label}
              </option>
            ))}
          </select>
        </div>
        <div className="rounded-xl border border-border bg-card-2 p-3">
          <div className="mb-3 flex items-center justify-between">
            <span className="text-xs font-semibold text-txt">规则引擎</span>
            <div className="flex gap-1 rounded-lg bg-card p-0.5 text-xs">
              <button type="button" onClick={() => setMode('single')} className={`rounded px-2 py-0.5 ${mode === 'single' ? 'bg-primary text-white' : 'text-muted'}`}>
                单条件
              </button>
              <button type="button" onClick={() => setMode('composite')} className={`rounded px-2 py-0.5 ${mode === 'composite' ? 'bg-primary text-white' : 'text-muted'}`}>
                组合条件
              </button>
            </div>
          </div>

          {mode === 'single' ? (
            <div className="space-y-3">
              <div>
                <label className="mb-1 block text-xs text-muted">触发规则</label>
                <select className="input" value={ruleType} onChange={(e) => setRuleType(e.target.value)}>
                  {RULE_TYPES.map((r) => (
                    <option key={r.key} value={r.key}>
                      {r.label}
                    </option>
                  ))}
                </select>
              </div>
              {RULE_TYPES_WITH_SUBJECT.has(ruleType) && (
                <div>
                  <label className="mb-1 block text-xs text-muted">规则目标（科目）</label>
                  <select className="input" value={ruleTarget} onChange={(e) => setRuleTarget(e.target.value)}>
                    <option value="">— 选择科目 —</option>
                    {subjects.map((s) => (
                      <option key={s.key} value={s.key}>
                        {s.emoji} {s.label} ({s.key})
                      </option>
                    ))}
                  </select>
                </div>
              )}
              <div>
                <div className="mb-1 flex items-center justify-between">
                  <label className="block text-xs text-muted">达标阈值</label>
                  <label className="flex cursor-pointer items-center gap-1 text-xs text-muted">
                    <input type="checkbox" checked={multiTier} onChange={(e) => setMultiTier(e.target.checked)} className="accent-primary" />
                    多层级（递进解锁）
                  </label>
                </div>
                {!multiTier ? (
                  <input type="number" className="input" value={threshold} onChange={(e) => setThreshold(Number(e.target.value))} required min={1} />
                ) : (
                  <div className="space-y-2">
                    <div className="grid grid-cols-[1fr_1fr_auto] gap-2 text-xs text-muted">
                      <span>阈值</span>
                      <span>奖励积分</span>
                      <span></span>
                    </div>
                    {tierRows.map((row, idx) => (
                      <div key={idx} className="grid grid-cols-[1fr_1fr_auto] gap-2">
                        <input type="number" className="input" value={row.t} onChange={(e) => setTierRows(tierRows.map((r, i) => i === idx ? { ...r, t: Number(e.target.value) } : r))} min={1} />
                        <input type="number" className="input" value={row.r} onChange={(e) => setTierRows(tierRows.map((r, i) => i === idx ? { ...r, r: Number(e.target.value) } : r))} min={0} />
                        <button type="button" className="btn-ghost px-2" onClick={() => setTierRows(tierRows.filter((_, i) => i !== idx))} disabled={tierRows.length <= 1}>✕</button>
                      </div>
                    ))}
                    <button type="button" className="btn-ghost w-full text-xs" onClick={() => setTierRows([...tierRows, { t: 0, r: 0 }])}>+ 添加层级</button>
                    <p className="text-xs text-muted">阈值需递增；每层解锁时发放对应奖励积分。层级可随时追加（已有进度不受影响）。</p>
                  </div>
                )}
              </div>
            </div>
          ) : (
            <CompositeEditor tree={tree} onChange={setTree} subjects={subjects} />
          )}
        </div>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : '保存'}
        </button>
      </form>
    </Modal>
  );
}

// collectLeaves flattens a composite tree into its leaf rules (for validation).
function collectLeaves(node: RuleNode): LeafRule[] {
  if (node.rules && node.rules.length > 0) {
    return node.rules.flatMap(collectLeaves);
  }
  return [{ type: node.type ?? '', target: node.target ?? '', threshold: node.threshold ?? 0 }];
}

// CompositeEditor renders the AND/OR rule tree. The root is always a group;
// users add/remove leaf conditions and toggle the join logic. Nested groups
// are supported (a rule may itself be a group) but the common case is a flat
// list of leaves under one logic.
function CompositeEditor({ tree, onChange, subjects }: { tree: RuleNode; onChange: (t: RuleNode) => void; subjects: { key: string; label: string; emoji: string }[] }) {
  const update = (next: RuleNode) => onChange({ ...next });
  const rules = tree.rules ?? [];

  const setLogic = (logic: 'and' | 'or') => update({ ...tree, logic });
  const addLeaf = () => update({ ...tree, rules: [...rules, { type: 'watch_duration', target: '', threshold: 10 }] });
  const removeAt = (i: number) => update({ ...tree, rules: rules.filter((_, idx) => idx !== i) });
  const editAt = (i: number, patch: Partial<RuleNode>) =>
    update({ ...tree, rules: rules.map((r, idx) => (idx === i ? { ...r, ...patch } : r)) });

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-xs text-muted">
        <span>满足方式：</span>
        <div className="flex gap-1 rounded-lg bg-card p-0.5">
          <button type="button" onClick={() => setLogic('and')} className={`rounded px-2 py-0.5 ${tree.logic === 'and' ? 'bg-primary text-white' : 'text-muted'}`}>
            全部满足（且）
          </button>
          <button type="button" onClick={() => setLogic('or')} className={`rounded px-2 py-0.5 ${tree.logic === 'or' ? 'bg-primary text-white' : 'text-muted'}`}>
            任一满足（或）
          </button>
        </div>
      </div>
      <div className="space-y-2">
        {rules.map((r, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card p-2">
            <select className="input !py-1 !text-xs flex-1 min-w-[140px]" value={r.type} onChange={(e) => editAt(i, { type: e.target.value })}>
              {RULE_TYPES.map((rt) => (
                <option key={rt.key} value={rt.key}>
                  {rt.label}
                </option>
              ))}
            </select>
            {r.type && RULE_TYPES_WITH_SUBJECT.has(r.type) && (
              <select className="input !py-1 !text-xs max-w-[140px]" value={r.target ?? ''} onChange={(e) => editAt(i, { target: e.target.value })}>
                <option value="">选科目</option>
                {subjects.map((s) => (
                  <option key={s.key} value={s.key}>
                    {s.emoji} {s.label}
                  </option>
                ))}
              </select>
            )}
            <span className="text-xs text-muted">≥</span>
            <input
              type="number"
              className="input !py-1 !text-xs w-20"
              value={r.threshold ?? 0}
              onChange={(e) => editAt(i, { threshold: Number(e.target.value) })}
              min={1}
            />
            <button type="button" className="btn-ghost btn-sm text-bad" onClick={() => removeAt(i)} title="移除该条件">
              ✕
            </button>
          </div>
        ))}
      </div>
      <button type="button" className="btn-secondary btn-sm" onClick={addLeaf}>
        + 添加条件
      </button>
      <p className="text-[11px] text-muted">
        例：连续7天 且 累计学习60分钟 → 满足方式选「全部满足」，加两个条件（consecutive_days≥7、watch_duration≥60）。
      </p>
    </div>
  );
}
