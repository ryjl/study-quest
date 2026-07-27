import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Plus, Lock, AlertTriangle, Tags } from 'lucide-react';
import { api } from '../lib/api';
import { useSubjects, useInvalidateSubjects } from '../lib/useSubjects';
import { useDeleteConfirm } from '../lib/useDeleteConfirm';
import type { SubjectMeta } from '../lib/types';
import { Modal, LoadingState, EmptyState, SubjectIcon } from '../components/ui';
import { resolveSubjectIcon } from '../lib/subjectIcon';
import { useToast } from '../lib/toast';
import { AIHintFields, emptyAiHintValue, type AiHintFieldsValue } from './ai-console/AIHintFields';

// Color swatch palette offered for new/custom subjects. The admin can also
// paste any hex value in the dedicated field.
const COLOR_CHOICES = [
  '#60a5fa', '#f59e0b', '#34d399', '#6366f1', '#f43f5e',
  '#06b6d4', '#ec4899', '#84cc16', '#eab308', '#64748b',
];

// Reusable table + create/edit modal for subjects. Embedded in the
// Classification page's "科目" tab(没有独立 /admin/subjects 路由,只在此处渲染)。
// The page-level title/description is owned by the host (Classification), so
// this component only renders the "+ 新增科目" action and the modal.
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

function SubjectModal({ subject, onClose }: { subject: SubjectMeta | null; onClose: () => void }) {
  const isEdit = !!subject;
  const toast = useToast();
  const invalidate = useInvalidateSubjects();

  const [key, setKey] = useState('');
  const [label, setLabel] = useState('');
  const [color, setColor] = useState(COLOR_CHOICES[0]);
  const [sortOrder, setSortOrder] = useState(0);
  // Category: academic (学习课用) or entertainment (娱乐课用). The backend
  // stores this on Subject.Category and uses it to filter the CourseModal /
  // ImportDialog subject dropdowns by content type. Pre-2026-07-21 this field
  // was set only at seed time and never editable — admin-created subjects were
  // stuck at the default 'academic' forever, so an admin who created 动画片
  // then realized they'd miscategorized it had to delete + recreate. The
  // backend handler (subject_handler.go) already accepts category on PUT; the
  // form just wasn't sending it.
  const [category, setCategory] = useState<'academic' | 'entertainment'>('academic');
  // 学科级 AI 提示(5 字段 hint)。2026-07-26 从 AI 控制台 Prompt tab 挪回这里——
  // 学科和它的 AI 默认本就该在一起编辑(之前跳出去配是集中化做一半的产物)。
  // 学科是 AI 提示的"模板源",课程覆盖会回退到这里,所以放学科定义处最自然。
  const [aiCfg, setAiCfg] = useState<AiHintFieldsValue>(emptyAiHintValue());

  useEffect(() => {
    if (subject) {
      setKey(subject.key);
      setLabel(subject.label);
      setColor(subject.color || COLOR_CHOICES[0]);
      setSortOrder(subject.sort_order ?? 0);
      setCategory(subject.category === 'entertainment' ? 'entertainment' : 'academic');
      // 回填已存在的 ai_config(5 字段)。空字段兜底为空串,AIHintFields 容忍。
      const c = subject.ai_config;
      setAiCfg({
        whisper_hint: c?.whisper_hint ?? '',
        summary_hint: c?.summary_hint ?? '',
        quiz_hint: c?.quiz_hint ?? '',
        advice_hint: c?.advice_hint ?? '',
        term_dict: c?.term_dict ?? '',
      });
    } else {
      setKey('');
      setLabel('');
      setColor(COLOR_CHOICES[0]);
      setSortOrder(0);
      setCategory('academic');
      setAiCfg(emptyAiHintValue());
    }
  }, [subject]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        key: key.trim().toLowerCase(),
        label: label.trim(),
        color,
        sort_order: sortOrder,
        category,
        // 本表单直接编辑 5 字段 hint,save 时发完整 ai_config。
        ai_config: {
          whisper_hint: aiCfg.whisper_hint.trim(),
          summary_hint: aiCfg.summary_hint.trim(),
          quiz_hint: aiCfg.quiz_hint.trim(),
          advice_hint: aiCfg.advice_hint.trim(),
          term_dict: aiCfg.term_dict.trim(),
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
          <label className="mb-1 block text-xs text-muted">分类（决定该科目出现在哪种课程的下拉里）</label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => setCategory('academic')}
              className={`flex-1 rounded-md border px-3 py-2 text-sm transition-colors ${category === 'academic' ? 'border-txt bg-card-2 text-txt font-medium' : 'border-border text-muted hover:text-txt'}`}
            >
              学术（学习课用）
            </button>
            <button
              type="button"
              onClick={() => setCategory('entertainment')}
              className={`flex-1 rounded-md border px-3 py-2 text-sm transition-colors ${category === 'entertainment' ? 'border-txt bg-card-2 text-txt font-medium' : 'border-border text-muted hover:text-txt'}`}
            >
              娱乐（娱乐课用）
            </button>
          </div>
          <p className="mt-1 text-[11px] text-muted">
            注意：切换分类不会自动迁移已用该科目的课程。已建好的课程仍保留原 ContentType，需逐个编辑。
          </p>
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

        {/* 学科级 AI 提示(5 字段 hint)。学科是 AI 提示的"模板源"——课程未覆盖时
            回退到这里,所以它就该在学科定义处编辑。复用 AIHintFields(和课程覆盖、
            原 Prompt tab 同一套表单,标签/帮助文案一致)。学科本身就是模板源,所以
            不显示"套用模板"按钮(那对学科是循环的)。 */}
        <div>
          <label className="mb-1 block text-xs text-muted">学科默认 AI 提示（课程未覆盖时回退到这里）</label>
          <AIHintFields value={aiCfg} onChange={setAiCfg} />
        </div>

        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '创建科目'}
        </button>
      </form>
    </Modal>
  );
}
