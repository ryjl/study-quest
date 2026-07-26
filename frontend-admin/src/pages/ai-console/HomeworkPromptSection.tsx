import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { RotateCcw } from 'lucide-react';
import { api } from '../../lib/api';
import { useToast } from '../../lib/toast';
import { useConfirm } from '../../lib/toast';
import { useSubjects } from '../../lib/useSubjects';

// HomeworkPromptSection — Prompt 配置 tab 的第 3 段:作业生成 prompt。
//
// 与上方 SubjectPromptSection(5 字段 ai_config)的区别:这里管的是该 subject 的
// 完整作业生成 system_prompt(后端独立表/列,跟 ai_config 不重叠)。后端首次 GET 时
// lazy 灌默认,所以总能取到。
//
// 端点(都带 ?key=<subject_key>):
//   GET  /admin/api/ai/subjects/:id/homework-prompt       → HomeworkPromptConfig
//   PUT  /admin/api/ai/subjects/:id/homework-prompt       → {ok:true}  body {system_prompt}
//   POST /admin/api/ai/subjects/:id/homework-prompt/reset → {ok:true}
//
// key 从选中 subject 的 key 字段拿(SubjectMeta.key)。
//
// 提示文案:"这是该科目的完整作业生成 prompt。修改后新生成的作业会用新 prompt;
// 已生成的作业不受影响。"

export function HomeworkPromptSection() {
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];
  const toast = useToast();
  const confirm = useConfirm();
  const qc = useQueryClient();

  const [subjectId, setSubjectId] = useState<number | null>(null);
  const [prompt, setPrompt] = useState('');
  const [updatedAt, setUpdatedAt] = useState<string>('');

  const selectedSubject = subjects.find((s) => s.id === subjectId);
  const subjectKey = selectedSubject?.key ?? '';

  // 拉当前 subject 的 prompt。queryKey 带 subjectId + key,切 subject 自动重取。
  const promptQ = useQuery({
    queryKey: ['homework-prompt', subjectId, subjectKey],
    queryFn: () => api.homeworkGetPrompt(subjectId as number, subjectKey),
    enabled: subjectId != null && !!subjectKey,
  });

  // 拉到数据后同步到本地编辑态(只在 subjectId 变化时同步一次,避免编辑中被覆盖)。
  useEffect(() => {
    if (promptQ.data) {
      setPrompt(promptQ.data.system_prompt);
      setUpdatedAt(promptQ.data.updated_at);
    } else if (!promptQ.isLoading && subjectId == null) {
      setPrompt('');
      setUpdatedAt('');
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [promptQ.data, subjectId]);

  const saveMut = useMutation({
    mutationFn: () => api.homeworkSavePrompt(subjectId as number, subjectKey, prompt),
    onSuccess: () => {
      toast.success('作业生成 Prompt 已保存');
      qc.invalidateQueries({ queryKey: ['homework-prompt', subjectId, subjectKey] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  const resetMut = useMutation({
    mutationFn: () => api.homeworkResetPrompt(subjectId as number, subjectKey),
    onSuccess: () => {
      toast.success('已恢复默认作业 Prompt');
      qc.invalidateQueries({ queryKey: ['homework-prompt', subjectId, subjectKey] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '重置失败'),
  });

  const onReset = async () => {
    const ok = await confirm({
      message: '恢复默认作业 Prompt?',
      detail: '该科目的作业生成 Prompt 会被重置为系统默认值。已生成的作业不受影响。',
      danger: true,
    });
    if (ok) resetMut.mutate();
  };

  // 检测是否有未保存改动(用于禁用/启用保存按钮)。
  const dirty = prompt !== (promptQ.data?.system_prompt ?? '');

  if (subjectsQ.isLoading) {
    return (
      <section className="space-y-3 rounded-lg border border-border bg-card p-4">
        <h2 className="text-base font-semibold">作业生成 Prompt</h2>
        <div className="px-1 py-6 text-sm text-muted">加载中…</div>
      </section>
    );
  }
  if (subjectsQ.error) {
    return (
      <section className="space-y-3 rounded-lg border border-border bg-card p-4">
        <h2 className="text-base font-semibold">作业生成 Prompt</h2>
        <div className="space-y-2">
          <div className="text-sm text-bad">加载失败: {(subjectsQ.error as Error).message}</div>
          <button className="btn-secondary btn-sm" onClick={() => subjectsQ.refetch()}>
            重试
          </button>
        </div>
      </section>
    );
  }

  return (
    <section className="space-y-3 rounded-lg border border-border bg-card p-4">
      <header className="space-y-0.5">
        <h2 className="text-base font-semibold">作业生成 Prompt</h2>
        <p className="text-xs text-muted">
          这是该科目的完整作业生成 prompt。修改后新生成的作业会用新 prompt;已生成的作业不受影响。
        </p>
      </header>

      <div>
        <label className="mb-1 block text-xs text-muted">选择学科</label>
        <select
          className="input max-w-md"
          value={subjectId ?? ''}
          onChange={(e) => setSubjectId(e.target.value ? Number(e.target.value) : null)}
        >
          <option value="">— 请选择 —</option>
          {subjects.map((s) => (
            <option key={s.id} value={s.id}>
              {s.label}（{s.key}）{s.is_system ? ' · 系统' : ''}
            </option>
          ))}
        </select>
      </div>

      {selectedSubject ? (
        <>
          {promptQ.isLoading ? (
            <div className="px-1 py-6 text-sm text-muted">加载 Prompt…</div>
          ) : promptQ.isError ? (
            <div className="space-y-2">
              <div className="text-sm text-bad">加载 Prompt 失败: {(promptQ.error as Error).message}</div>
              <button className="btn-secondary btn-sm" onClick={() => promptQ.refetch()}>
                重试
              </button>
            </div>
          ) : (
            <>
              <div>
                <label className="mb-1 block text-xs text-muted">System Prompt</label>
                <textarea
                  className="input font-mono text-xs"
                  rows={14}
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  placeholder="该科目的作业生成 system prompt…"
                />
                {updatedAt && (
                  <div className="mt-1 text-[11px] text-muted">最后更新:{updatedAt}</div>
                )}
              </div>

              <div className="flex items-center justify-between gap-2">
                <button
                  type="button"
                  className="btn-ghost btn-sm inline-flex items-center gap-1.5"
                  onClick={onReset}
                  disabled={resetMut.isPending}
                  title="恢复成系统默认 prompt"
                >
                  <RotateCcw size={14} />
                  {resetMut.isPending ? '重置中…' : '恢复默认'}
                </button>
                <button
                  className="btn-primary"
                  onClick={() => saveMut.mutate()}
                  disabled={saveMut.isPending || !dirty}
                >
                  {saveMut.isPending ? '保存中…' : '保存'}
                </button>
              </div>
            </>
          )}
        </>
      ) : (
        <div className="rounded-md border border-dashed border-border bg-card-2 px-4 py-8 text-center text-sm text-muted">
          选择一个学科以编辑其作业生成 Prompt。
        </div>
      )}
    </section>
  );
}
