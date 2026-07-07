import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { AdminBadge } from '../lib/types';
import { useSubjects } from '../lib/useSubjects';
import { Modal, LoadingState, EmptyState } from '../components/ui';
import { useToast, useConfirm } from '../lib/toast';

const ICONS = [
  { key: 'badge_first_blood', emoji: '✨', label: '首战告捷' },
  { key: 'badge_streak_7', emoji: '🔥', label: '七日先锋' },
  { key: 'badge_math', emoji: '🧮', label: '数学达人' },
  { key: 'badge_english', emoji: '🗣️', label: '英语之星' },
  { key: 'badge_night_owl', emoji: '🦉', label: '夜猫学者' },
  { key: 'badge_gold', emoji: '🏆', label: '黄金大满贯' },
];

const RULE_TYPES = [
  { key: 'watch_duration', label: '累计学习时长（分钟）' },
  { key: 'consecutive_days', label: '连续活跃天数（天）' },
  { key: 'subject_count', label: '特定科目完课数（课时）' },
  { key: 'night_owl_count', label: '深夜听课次数' },
  { key: 'points_earned', label: '累计获得星币' },
];

function emojiFor(iconName: string | undefined) {
  if (!iconName) return '🏅';
  return ICONS.find((i) => iconName.includes(i.key.split('_')[1]))?.emoji ?? '🏅';
}

export function Badges() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<AdminBadge | null>(null);
  const [creating, setCreating] = useState(false);

  const badgesQ = useQuery({ queryKey: ['badges'], queryFn: api.listBadges });
  const badges = badgesQ.data ?? [];

  const delMut = useMutation({
    mutationFn: api.deleteBadge,
    onSuccess: () => {
      toast.success('徽章已删除');
      qc.invalidateQueries({ queryKey: ['badges'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <div>
      <div className="mb-6 flex items-center justify-between border-b border-border pb-4">
        <h1 className="text-2xl font-bold text-txt">荣誉徽章</h1>
        <button className="btn-primary" onClick={() => setCreating(true)}>
          + 新增勋章
        </button>
      </div>

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
                  <div className="mb-2 text-4xl">{emojiFor(b.IconName)}</div>
                  <strong className="text-txt">{b.Title}</strong>
                  <span className="mb-3 mt-1 line-clamp-2 h-8 text-xs text-muted">{b.Description || '暂无说明'}</span>
                  <div className="mb-3 w-full rounded-md border border-border bg-card-2 px-2 py-1 text-[10px] text-primary">
                    {b.RuleType}
                    {b.RuleTarget ? ` (${b.RuleTarget})` : ''} ≥ {b.Threshold}
                  </div>
                  <div className="flex w-full gap-1.5">
                    <button className="btn-secondary btn-sm flex-1" onClick={() => setEditing(b)}>
                      编辑
                    </button>
                    <button
                      className="btn-danger btn-sm flex-1"
                      onClick={async () => {
                        if (await confirm({ message: `删除「${b.Title}」徽章？`, detail: '将清除所有学生的解锁状态。', danger: true })) delMut.mutate(b.ID);
                      }}
                    >
                      删除
                    </button>
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
            <li><strong className="text-primary">特定科目完课数</strong> subject_count（需填 rule_target）</li>
            <li><strong className="text-warn">夜猫学者次数</strong> night_owl_count</li>
            <li><strong className="text-bad">累计星币</strong> points_earned</li>
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
            qc.invalidateQueries({ queryKey: ['badges'] });
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
  const [ruleType, setRuleType] = useState('watch_duration');
  const [ruleTarget, setRuleTarget] = useState('');
  const [threshold, setThreshold] = useState(1);

  useEffect(() => {
    if (badge) {
      setCode(badge.Code);
      setTitle(badge.Title);
      setDescription(badge.Description);
      setIconName(badge.IconName);
      setRuleType(badge.RuleType);
      setRuleTarget(badge.RuleTarget);
      setThreshold(badge.Threshold);
    }
  }, [badge]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = { code, title, description, icon_name: iconName, rule_type: ruleType, rule_target: ruleTarget, threshold };
      if (isEdit && badge) return api.updateBadge(badge.ID, body);
      return api.createBadge(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '徽章已更新' : '徽章已创建');
      onSaved();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑徽章' : '创建徽章'} size="md">
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
          <div className="mb-3 text-xs font-semibold text-txt">规则引擎</div>
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
            {ruleType === 'subject_count' && (
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
              <label className="mb-1 block text-xs text-muted">达标阈值</label>
              <input type="number" className="input" value={threshold} onChange={(e) => setThreshold(Number(e.target.value))} required min={1} />
            </div>
          </div>
        </div>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : '保存'}
        </button>
      </form>
    </Modal>
  );
}
