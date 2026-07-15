import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { Episode } from '../../lib/types';
import { Drawer, LoadingState, EmptyState } from '../../components/ui';
import { useToast, useConfirm } from '../../lib/toast';

/** A single subtitle row with an expandable content preview. */
function SubtitleRow({ id, language, label, onDelete }: { id: number; language: string; label: string; onDelete: () => void }) {
  const [open, setOpen] = useState(false);
  // Fetch the SRT content only when the row is first expanded (lazy), and cache
  // it so re-opening is instant. The list endpoint omits srt_content on purpose.
  const contentQ = useQuery({
    queryKey: ['subtitle-content', id],
    queryFn: () => api.getSubtitle(id),
    enabled: open,
    staleTime: Infinity,
  });

  return (
    <div className="rounded-lg border border-border bg-card-2 px-3 py-2 text-sm">
      <div className="flex items-center justify-between">
        <div>
          <span className="rounded bg-good/20 px-1.5 py-0.5 text-xs text-good">{language}</span>
          <strong className="ml-2 text-txt">{label}</strong>
        </div>
        <div className="flex gap-1.5">
          <button className="btn-ghost btn-sm" onClick={() => setOpen((v) => !v)}>
            {open ? '收起' : '查看'}
          </button>
          <button className="btn-danger btn-sm" onClick={onDelete}>删除</button>
        </div>
      </div>
      {open && (
        <div className="mt-2">
          {contentQ.isLoading ? (
            <div className="py-4 text-center text-xs text-muted">加载中…</div>
          ) : contentQ.isError ? (
            <div className="py-4 text-center text-xs text-bad">加载失败</div>
          ) : (
            <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded bg-black/5 p-2 text-xs leading-relaxed text-txt">
              {contentQ.data?.srt_content || '(空)'}
            </pre>
          )}
        </div>
      )}
    </div>
  );
}

export function SubtitleDrawer({ episode, onClose }: { episode: Episode; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const fileRef = useRef<HTMLInputElement>(null);

  const [lang, setLang] = useState('zh-CN');
  const [label, setLabel] = useState('中文');
  const [fileName, setFileName] = useState('');
  const [content, setContent] = useState('');

  const subsQ = useQuery({ queryKey: ['subtitles', episode.id], queryFn: () => api.listSubtitles(episode.id) });
  const subs = subsQ.data ?? [];

  const saveMut = useMutation({
    mutationFn: async () => {
      if (!content) throw new Error('请选择字幕文件或等待读取完毕');
      return api.saveSubtitle(episode.id, { language: lang, label, srt_content: content });
    },
    onSuccess: () => {
      toast.success('字幕上传成功');
      qc.invalidateQueries({ queryKey: ['subtitles', episode.id] });
      setContent('');
      setFileName('');
      if (fileRef.current) fileRef.current.value = '';
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const delMut = useMutation({
    mutationFn: api.deleteSubtitle,
    onSuccess: () => {
      toast.success('字幕已删除');
      qc.invalidateQueries({ queryKey: ['subtitles', episode.id] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const onFile = (file: File) => {
    setFileName(file.name);
    const reader = new FileReader();
    reader.onload = (e) => setContent((e.target?.result as string) ?? '');
    reader.readAsText(file);
  };

  return (
    <Drawer open onClose={onClose} title={`字幕管理 · ${episode.title}`} width="md">
      <section className="mb-6">
        <h3 className="mb-2 text-sm font-semibold text-txt">已有字幕</h3>
        {subsQ.isLoading ? (
          <LoadingState />
        ) : subs.length === 0 ? (
          <EmptyState icon="💬" title="暂无字幕" hint="在下方上传 .srt 或 .vtt 文件" />
        ) : (
          <div className="space-y-2">
            {subs.map((s) => (
              <SubtitleRow
                key={s.id}
                id={s.id}
                language={s.language}
                label={s.label}
                onDelete={async () => {
                  if (await confirm({ message: `删除「${s.label}」字幕？`, danger: true })) delMut.mutate(s.id);
                }}
              />
            ))}
          </div>
        )}
      </section>

      <section>
        <h3 className="mb-2 text-sm font-semibold text-txt">上传新字幕 (.srt / .vtt)</h3>
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="mb-1 block text-xs text-muted">语言代码</label>
              <input className="input" value={lang} onChange={(e) => setLang(e.target.value)} placeholder="zh-CN" />
            </div>
            <div>
              <label className="mb-1 block text-xs text-muted">显示名称</label>
              <input className="input" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="中文" />
            </div>
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">字幕文件</label>
            <input
              ref={fileRef}
              type="file"
              accept=".srt,.vtt"
              className="input !py-2"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) onFile(f);
              }}
            />
            {fileName && <div className="mt-1 text-xs text-good">✓ 已读取: {fileName}</div>}
          </div>
          <button className="btn-primary w-full" onClick={() => saveMut.mutate()} disabled={saveMut.isPending || !content}>
            {saveMut.isPending ? '上传中...' : '开始上传'}
          </button>
        </div>
      </section>
    </Drawer>
  );
}
