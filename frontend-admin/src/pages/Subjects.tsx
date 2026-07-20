import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Plus, Lock, AlertTriangle, Tags } from 'lucide-react';
import { api } from '../lib/api';
import { useSubjects, useInvalidateSubjects } from '../lib/useSubjects';
import { useDeleteConfirm } from '../lib/useDeleteConfirm';
import type { SubjectMeta } from '../lib/types';
import { Modal, LoadingState, EmptyState, SubjectIcon } from '../components/ui';
import { resolveSubjectIcon } from '../lib/subjectIcon';
import { useToast } from '../lib/toast';

// Color swatch palette offered for new/custom subjects. The admin can also
// paste any hex value in the dedicated field.
const COLOR_CHOICES = [
  '#60a5fa', '#f59e0b', '#34d399', '#6366f1', '#f43f5e',
  '#06b6d4', '#ec4899', '#84cc16', '#eab308', '#64748b',
];

// Reusable table + create/edit modal for subjects. Rendered standalone by the
// legacy /admin/subjects route (kept for safety) and embedded in the
// Classification page's "科目" tab. The page-level title/description is owned
// by the host (Subjects page or Classification), so this component only
// renders the "+ 新增科目" action (right-aligned above the table) and the modal.
export function SubjectsTable() {
  const subjectsQ = useSubjects();
  const invalidate = useInvalidateSubjects();

  const [editing, setEditing] = useState<SubjectMeta | null>(null);
  const [creating, setCreating] = useState(false);

  const del = useDeleteConfirm({
    mutationFn: api.deleteSubject,
    noun: '科目',
    onDeleted: invalidate,
  });

  const onDelete = (s: SubjectMeta) =>
    del.confirmAndDelete(s.id!, `确认删除科目「${s.label}」？`, '若该科目下仍有课程，删除将被拒绝。');

  if (subjectsQ.isLoading) return <LoadingState />;
  const subjects = subjectsQ.data ?? [];

  return (
    <div>
      <div className="mb-4 flex justify-end">
        <button className="btn-primary inline-flex items-center gap-1.5" onClick={() => setCreating(true)}><Plus size={14} /> 新增科目</button>
      </div>

      {subjects.length === 0 ? (
        <EmptyState icon={<Tags size={28} />} title="还没有科目" hint="新增第一个科目以开始分类课程。" />
      ) : (
        <div className="card overflow-hidden p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted">
                <th className="px-4 py-3 font-medium">科目</th>
                <th className="px-4 py-3 font-medium">Key</th>
                <th className="px-4 py-3 font-medium">分类</th>
                <th className="px-4 py-3 font-medium">颜色</th>
                <th className="px-4 py-3 font-medium">排序</th>
                <th className="px-4 py-3 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {subjects.map((s) => (
                <tr key={s.id ?? s.key} className="border-b border-border last:border-0 hover:bg-card-2/50">
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center gap-2 font-medium text-txt">
                      <SubjectIcon subject={s.key} size={16} />
                      {s.label}
                      {s.is_system && (
                        <span title="系统默认科目，不可删除（可在编辑里改名/改色）" className="text-muted"><Lock size={12} /></span>
                      )}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <code className="rounded bg-card-2 px-1.5 py-0.5 text-xs text-muted">{s.key}</code>
                  </td>
                  <td className="px-4 py-3">
                    {/* 分类:academic=学术学科(学习课用),entertainment=娱乐子类(娱乐课用)。
                        没标 category 的老数据按 academic 显示(后端 default)。 */}
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs ${
                        s.category === 'entertainment'
                          ? 'bg-purple-100 text-purple-700'
                          : 'bg-blue-100 text-blue-700'
                      }`}
                    >
                      {s.category === 'entertainment' ? '娱乐' : '学术'}
                    </span>
                  </td>
                  <td className="px-4 py-3">
                    <span className="inline-flex items-center gap-2">
                      <span className="h-4 w-4 rounded-full" style={{ backgroundColor: s.color }} />
                      <span className="text-xs text-muted">{s.color}</span>
                    </span>
                  </td>
                  <td className="px-4 py-3 text-muted">{s.sort_order ?? '-'}</td>
                  <td className="px-4 py-3 text-right">
                    <button className="btn-ghost btn-sm" onClick={() => setEditing(s)}>编辑</button>
                    {s.is_system ? (
                      <button className="btn-ghost btn-sm opacity-40" disabled title="系统默认科目，不可删除">
                        删除
                      </button>
                    ) : (
                      <button
                        className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                        onClick={() => onDelete(s)}
                        disabled={del.isPending}
                      >
                        删除
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {(creating || editing) && (
        <SubjectModal
          subject={editing}
          onClose={() => {
            setCreating(false);
            setEditing(null);
          }}
        />
      )}
    </div>
  );
}

// Legacy default export. The route is removed in App.tsx but kept here so any
// stray reference still resolves; it simply renders the shared table.
export function Subjects() {
  return <SubjectsTable />;
}

function SubjectModal({ subject, onClose }: { subject: SubjectMeta | null; onClose: () => void }) {
  const isEdit = !!subject;
  const toast = useToast();
  const navigate = useNavigate();
  const invalidate = useInvalidateSubjects();

  const [key, setKey] = useState('');
  const [label, setLabel] = useState('');
  const [color, setColor] = useState(COLOR_CHOICES[0]);
  const [sortOrder, setSortOrder] = useState(0);
  // 学科级 AI 提示(5 字段 hint)已迁移到「AI 控制台 → Prompt 配置」tab。
  // 这里只保留学科基本信息(key/label/color/sort_order)。save 时 ai_config 原值回传。

  useEffect(() => {
    if (subject) {
      setKey(subject.key);
      setLabel(subject.label);
      setColor(subject.color || COLOR_CHOICES[0]);
      setSortOrder(subject.sort_order ?? 0);
    } else {
      setKey('');
      setLabel('');
      setColor(COLOR_CHOICES[0]);
      setSortOrder(0);
    }
  }, [subject]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        key: key.trim().toLowerCase(),
        label: label.trim(),
        color,
        sort_order: sortOrder,
        // ai_config 原值回传 —— 本表单不再编辑 hint(已挪到 AI 控制台)。回传保证
        // PUT 不把这 5 字段误清。新建学科时 subject 为 null,ai_config 为 undefined。
        ai_config: subject?.ai_config,
      };
      if (!body.key) throw new Error('请填写科目 Key（小写英文）');
      if (!body.label) throw new Error('请填写科目名称');
      if (isEdit && subject?.id) return api.updateSubject(subject.id, body);
      return api.createSubject(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '科目已更新' : '科目已创建');
      invalidate();
      onClose();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑科目' : '新增科目'} size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">名称（显示名）</label>
          <input className="input" placeholder="如：数学" value={label} onChange={(e) => setLabel(e.target.value)} required autoFocus />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">
            Key（稳定标识，{isEdit ? '修改会级联更新相关徽章规则' : '小写英文/数字'}）
          </label>
          <input
            className="input"
            placeholder="如：math"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            required
            spellCheck={false}
          />
          {isEdit && (
            <p className="mt-1 inline-flex items-center gap-1 text-xs text-warn">
              <AlertTriangle size={14} /> 修改 Key 会同步更新使用它的徽章规则目标（subject_count）。
            </p>
          )}
        </div>
        {/* 图标预览：图标由 Key 自动映射（math→计算器、english→语言 等），
            不是手选。这里实时预览当前 Key 对应的 lucide 图标，让 admin 看到
            选对 Key 后图标长什么样。自定义/未识别 Key 回退到通用书本图标。
            参见 lib/subjectIcon.tsx 的完整映射表。 */}
        <div>
          <label className="mb-1 block text-xs text-muted">图标预览（由 Key 自动决定）</label>
          <div className="flex items-center gap-3 rounded-lg border border-border bg-card-2 px-3 py-2.5">
            <span
              className="flex h-10 w-10 items-center justify-center rounded-md"
              style={{ backgroundColor: `${color}1a`, color }}
            >
              {(() => {
                const PreviewIcon = resolveSubjectIcon(key.trim().toLowerCase());
                return <PreviewIcon size={20} />;
              })()}
            </span>
            <span className="text-xs text-muted">
              {key.trim() ? (
                <>Key「<code className="rounded bg-card px-1 py-0.5 text-txt">{key.trim().toLowerCase()}</code>」对应的图标。已知科目（math/english/physics 等）有专用图标，其他回退到通用书本。</>
              ) : (
                <>先填写 Key，图标会在这里预览。</>
              )}
            </span>
          </div>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">颜色</label>
          <div className="flex flex-wrap items-center gap-1.5">
            {COLOR_CHOICES.map((c) => (
              <button
                type="button"
                key={c}
                onClick={() => setColor(c)}
                className={`h-7 w-7 rounded-full border-2 transition ${color === c ? 'border-txt' : 'border-transparent'}`}
                style={{ backgroundColor: c }}
                aria-label={c}
              />
            ))}
            <input
              className="input ml-2 w-28 py-1 text-xs"
              placeholder="#custom"
              value={color}
              onChange={(e) => setColor(e.target.value)}
            />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">排序权重（数字越小越靠前）</label>
          <input
            className="input"
            type="number"
            value={sortOrder}
            onChange={(e) => setSortOrder(Number(e.target.value))}
          />
        </div>

        {/* 学科级 AI 提示已迁移到「AI 控制台 → Prompt 配置」tab。这里只留跳转入口,
            让 admin 知道该去哪配;学科基本信息(key/label/color/sort_order)仍在这里编辑。 */}
        <div className="flex items-center justify-between rounded-xl border border-border bg-card-2 p-3">
          <div>
            <div className="text-xs font-medium text-txt">学科默认 AI 提示</div>
            <div className="mt-0.5 text-[11px] text-muted">5 字段 hint(Whisper/总结/出题/建议/术语字典),课程未覆盖时回退到这里。集中管理。</div>
          </div>
          <button
            type="button"
            onClick={() => {
              if (!isEdit || !subject) {
                toast.info('请先创建学科,再配置 AI 提示');
                return;
              }
              // 跳到 AI 控制台 Prompt 配置 tab,带 subject id 让目标页预选该学科。
              // 用 navigate(SPA 内跳转,不刷页,保留 react-query 缓存)而不是
              // window.location.assign(后者会全页重载,体验差且丢缓存)。
              onClose();
              const subjectParam = subject.id ? `&subject=${subject.id}` : '';
              navigate(`/admin/ai-console?tab=prompt${subjectParam}`);
            }}
            className="flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-[11px] text-muted transition-colors hover:border-primary hover:text-primary"
            title="跳转到 AI 控制台 配置该学科的 AI 提示默认值"
          >
            配置 →
          </button>
        </div>

        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '创建科目'}
        </button>
      </form>
    </Modal>
  );
}
