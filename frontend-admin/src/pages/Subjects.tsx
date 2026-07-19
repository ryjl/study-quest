import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Plus, Lock, AlertTriangle, Tags, ChevronRight } from 'lucide-react';
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
  const invalidate = useInvalidateSubjects();

  const [key, setKey] = useState('');
  const [label, setLabel] = useState('');
  const [color, setColor] = useState(COLOR_CHOICES[0]);
  const [sortOrder, setSortOrder] = useState(0);
  // 学科级默认 AI 提示(5 字段)。课程级对应字段为空时回退到这里。默认折叠——
  // 学科级配置不是每次都改,展开后填入即可。state 复用 SubjectMeta.ai_config
  // 的 5 个 string。详见 backend model.Subject.AIConfig / Course.Effective*Hint。
  const [aiOpen, setAiOpen] = useState(false);
  const [whisperHint, setWhisperHint] = useState('');
  const [summaryHint, setSummaryHint] = useState('');
  const [quizHint, setQuizHint] = useState('');
  const [adviceHint, setAdviceHint] = useState('');
  const [termDict, setTermDict] = useState('');

  useEffect(() => {
    if (subject) {
      setKey(subject.key);
      setLabel(subject.label);
      setColor(subject.color || COLOR_CHOICES[0]);
      setSortOrder(subject.sort_order ?? 0);
      // 从 ai_config 回填 5 字段(后端 DTO 平铺回显,类型层主会话会加 ai_config)。
      const cfg = subject.ai_config;
      setWhisperHint(cfg?.whisper_hint ?? '');
      setSummaryHint(cfg?.summary_hint ?? '');
      setQuizHint(cfg?.quiz_hint ?? '');
      setAdviceHint(cfg?.advice_hint ?? '');
      setTermDict(cfg?.term_dict ?? '');
    } else {
      setKey('');
      setLabel('');
      setColor(COLOR_CHOICES[0]);
      setSortOrder(0);
      setWhisperHint('');
      setSummaryHint('');
      setQuizHint('');
      setAdviceHint('');
      setTermDict('');
    }
  }, [subject]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        key: key.trim().toLowerCase(),
        label: label.trim(),
        color,
        sort_order: sortOrder,
        // 5 字段整体提交。后端 handler 看到 ai_config 非 nil 就覆盖;全空即清空。
        ai_config: {
          whisper_hint: whisperHint.trim(),
          summary_hint: summaryHint.trim(),
          quiz_hint: quizHint.trim(),
          advice_hint: adviceHint.trim(),
          term_dict: termDict.trim(),
        },
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

        {/* 学科级默认 AI 提示:折叠区。该学科下所有课程未单独覆盖对应字段时回退到这里。
            term_dict 特殊:课程级会"追加"到学科级后面(合并而非覆盖)。
            默认折叠——不是每次都改,展开后填入即可。空字段 = 不设置默认。 */}
        <div className="rounded-xl border border-border bg-card-2">
          <button
            type="button"
            onClick={() => setAiOpen((v) => !v)}
            className="flex w-full items-center justify-between px-3 py-2.5 text-left text-xs font-medium text-muted hover:text-txt"
          >
            <span>学科默认 AI 提示（可选，课程未覆盖时回退到这里）</span>
            <ChevronRight size={14} className={`transition-transform ${aiOpen ? 'rotate-90' : ''}`} />
          </button>
          {aiOpen && (
            <div className="space-y-3 border-t border-border p-3">
              <div>
                <label className="mb-1 block text-[11px] text-muted">Whisper 提示（喂字幕转录，术语/口音，≤240 字）</label>
                <textarea
                  className="input min-h-[56px] resize-y"
                  placeholder="如：象棋术语：车马炮兵卒将帅士仕相象，屏风马，中炮。老师带南方口音。"
                  value={whisperHint}
                  onChange={(e) => setWhisperHint(e.target.value)}
                />
              </div>
              <div>
                <label className="mb-1 block text-[11px] text-muted">总结提示（喂 AI 总结，风格/侧重点）</label>
                <textarea
                  className="input min-h-[56px] resize-y"
                  placeholder="如：侧重开局原理，多举例题，避免堆砌术语。"
                  value={summaryHint}
                  onChange={(e) => setSummaryHint(e.target.value)}
                />
              </div>
              <div>
                <label className="mb-1 block text-[11px] text-muted">出题提示（喂出题 LLM，题型偏好/难度/出题指引）</label>
                <textarea
                  className="input min-h-[64px] resize-y"
                  placeholder={'如：题型倾向：计算题 ≥50% 出填空；难度偏难。'}
                  value={quizHint}
                  onChange={(e) => setQuizHint(e.target.value)}
                />
              </div>
              <div>
                <label className="mb-1 block text-[11px] text-muted">建议提示（喂建议 LLM，建议侧重点/口吻）</label>
                <textarea
                  className="input min-h-[56px] resize-y"
                  placeholder="如：象棋重实战练习，多鼓励；数学重计算巩固。"
                  value={adviceHint}
                  onChange={(e) => setAdviceHint(e.target.value)}
                />
              </div>
              <div>
                <label className="mb-1 block text-[11px] text-muted">术语字典（横切给总结/出题/建议，纠正字幕同音错字）</label>
                <textarea
                  className="input min-h-[56px] resize-y"
                  placeholder={'如：车→居（勿作居）、通分→同分、和棋→合棋。'}
                  value={termDict}
                  onChange={(e) => setTermDict(e.target.value)}
                />
                <p className="mt-1 text-[11px] text-muted">课程级术语字典会追加到本学科级后面(合并生效,不是覆盖)。</p>
              </div>
            </div>
          )}
        </div>

        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '创建科目'}
        </button>
      </form>
    </Modal>
  );
}
