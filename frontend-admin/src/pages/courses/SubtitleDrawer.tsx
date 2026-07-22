import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Captions, Check } from 'lucide-react';
import { api } from '../../lib/api';
import type { Episode, MediaStream } from '../../lib/types';
import { Drawer, LoadingState, EmptyState } from '../../components/ui';
import { SubtitleRow } from '../../components/SubtitleRow';
import { useToast, useConfirm } from '../../lib/toast';

/**
 * Map an ffprobe ISO 639-2 language tag (e.g. "chi", "eng", "und") to the
 * BCP-47 code our Subtitle.Language column expects ("zh-CN", "en-US"). Falls
 * back to the raw tag if unknown, and "und"/"" → "zh-CN" (most study-quest
 * content is Chinese, and an unlabeled embedded stream is overwhelmingly
 * Chinese in this catalog). This is just a smart default for the extract
 * button's payload — the admin can still edit the language on any saved row.
 */
function defaultLanguageForStream(lang?: string): string {
  const l = (lang ?? '').trim().toLowerCase();
  switch (l) {
    case '':
    case 'und': // ISO 639-2 "undetermined"
      return 'zh-CN';
    case 'chi':
    case 'zho':
    case 'zh':
    case 'cmn':
    case 'cmn-hans':
      return 'zh-CN';
    case 'eng':
    case 'en':
      return 'en-US';
    default:
      return l; // pass through (already lowercased short code)
  }
}

/** Parse the episode's media_meta_json. Returns null on missing/garbage input. */
function readSubtitleStreams(episode: Episode): MediaStream[] {
  if (!episode.media_meta_json) return [];
  try {
    const meta = JSON.parse(episode.media_meta_json);
    const streams = Array.isArray(meta?.streams) ? meta.streams : [];
    return streams.filter((s: MediaStream) => s?.type === 'subtitle');
  } catch {
    return [];
  }
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

  // PR3 — extract an embedded subtitle stream. On success the new row appears
  // in the "已有字幕" list above (same queryKey, invalidated). Bitmap-codec
  // failures (PGS/VOBSUB/DVB) arrive as a 400 ApiError with the "use Whisper"
  // hint already in message, so no special-case handling is needed here.
  const extractMut = useMutation({
    mutationFn: (vars: { streamIndex: number; language: string; label: string }) =>
      api.extractSubtitle(episode.id, {
        stream_index: vars.streamIndex,
        language: vars.language,
        label: vars.label,
      }),
    onSuccess: () => {
      toast.success('内嵌字幕提取成功');
      qc.invalidateQueries({ queryKey: ['subtitles', episode.id] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const subtitleStreams = readSubtitleStreams(episode);

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
          <EmptyState icon={<Captions size={28} />} title="暂无字幕" hint="在下方上传 .srt 或 .vtt 文件" />
        ) : (
          <div className="space-y-2">
            {subs.map((s) => (
              <SubtitleRow
                key={s.id}
                id={s.id}
                language={s.language}
                label={s.label}
                source={s.source}
                optimized={s.optimized}
                onDelete={async () => {
                  if (await confirm({ message: `删除「${s.label}」字幕？`, danger: true })) delMut.mutate(s.id);
                }}
              />
            ))}
          </div>
        )}
      </section>

      {subtitleStreams.length > 0 && (
        <section className="mb-6 border-t border-border pt-4">
          <h3 className="mb-2 text-sm font-semibold text-txt">
            内嵌字幕 ({subtitleStreams.length})
          </h3>
          <p className="mb-2 text-xs text-muted">
            从视频容器中直接抽取文本字幕流。图形字幕（PGS/VOBSUB/DVB）无法提取，需用 Whisper 转录。
          </p>
          <div className="space-y-2">
            {subtitleStreams.map((s) => {
              const lang = defaultLanguageForStream(s.language);
              const label = s.language ? `${s.language}` : '默认语言';
              const isExtracting =
                extractMut.isPending &&
                extractMut.variables?.streamIndex === s.index;
              return (
                <div
                  key={s.index}
                  className="flex items-center justify-between rounded-lg border border-border bg-card-2 px-3 py-2 text-sm"
                >
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-card px-1.5 py-0.5 text-xs text-muted">
                      #{s.index}
                    </span>
                    <span className="text-txt">{s.language || '未标语言'}</span>
                    {s.codec && (
                      <span className="rounded bg-card px-1.5 py-0.5 font-mono text-[10px] text-muted">
                        {s.codec}
                      </span>
                    )}
                  </div>
                  {s.is_bitmap ? (
                    <span className="text-xs text-bad">图形字幕，无法提取，请用 Whisper 转录</span>
                  ) : (
                    <button
                      className="btn-ghost btn-sm"
                      disabled={extractMut.isPending}
                      onClick={() => extractMut.mutate({ streamIndex: s.index, language: lang, label })}
                    >
                      {isExtracting ? '提取中...' : '提取'}
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        </section>
      )}

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
            {fileName && <div className="mt-1 inline-flex items-center gap-1 text-xs text-good"><Check size={12} /> 已读取: {fileName}</div>}
          </div>
          <button className="btn-primary w-full" onClick={() => saveMut.mutate()} disabled={saveMut.isPending || !content}>
            {saveMut.isPending ? '上传中...' : '开始上传'}
          </button>
        </div>
      </section>
    </Drawer>
  );
}
