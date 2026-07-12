import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { Chapter, Episode } from '../../lib/types';
import { Modal } from '../../components/ui';
import { PathBrowser } from '../../components/PathBrowser';
import { useToast } from '../../lib/toast';

export function EpisodeEditor({
  courseId,
  episode,
  chapters,
  defaultChapterId = 0,
  onClose,
  onSaved,
}: {
  courseId: number;
  episode: Episode | null;
  chapters: Chapter[];
  defaultChapterId?: number;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = !!episode;
  const toast = useToast();

  const [title, setTitle] = useState('');
  const [path, setPath] = useState('');
  const [chapterId, setChapterId] = useState(0);
  const [sortOrder, setSortOrder] = useState(1);
  const [browsing, setBrowsing] = useState(false);

  useEffect(() => {
    if (episode) {
      setTitle(episode.title);
      setPath(episode.video_relative_path);
      setChapterId(episode.chapter_id);
      setSortOrder(episode.sort_order);
    } else {
      setTitle('');
      setPath('');
      setChapterId(defaultChapterId);
      setSortOrder(1);
    }
  }, [episode, defaultChapterId]);

  const saveMut = useMutation({
    mutationFn: async () => {
      if (!title.trim()) throw new Error('请输入课时名称');
      if (!path.trim()) throw new Error('请输入视频相对路径');
      const body = { title: title.trim(), video_relative_path: path.trim(), chapter_id: chapterId, sort_order: sortOrder };
      if (isEdit && episode) return api.updateEpisode(episode.id, body);
      return api.createEpisode(courseId, body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '课时已更新' : '课时已添加');
      onSaved();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑课时' : '新增课时'} size="lg">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label className="mb-1 block text-xs text-muted">课时名称</label>
            <input className="input" value={title} onChange={(e) => setTitle(e.target.value)} required autoFocus placeholder="如：第1课 杠杆原理" />
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">关联章节</label>
            <select className="input" value={chapterId} onChange={(e) => setChapterId(Number(e.target.value))}>
              <option value={0}>默认 (未分类)</option>
              {chapters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.title}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">视频相对路径</label>
          <div className="flex gap-2">
            <input className="input font-mono" value={path} onChange={(e) => setPath(e.target.value)} required placeholder="/Physics/01.mp4" />
            <button
              type="button"
              className="btn-secondary whitespace-nowrap"
              onClick={() => setBrowsing(true)}
              title="从网盘浏览选择视频文件"
            >
              📁 浏览
            </button>
          </div>
        </div>
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
          <div>
            <label className="mb-1 block text-xs text-muted">排序值</label>
            <input type="number" className="input" value={sortOrder} onChange={(e) => setSortOrder(Number(e.target.value))} required min={1} />
          </div>
        </div>
        {isEdit && (
          <div className="rounded-lg border border-border bg-card-2 p-3 text-xs text-muted">
            <div className="mb-1 font-semibold text-txt">媒体信息（只读 · 由 ffprobe 自动探测）</div>
            <div className="grid grid-cols-2 gap-1 font-mono">
              <span>时长：{episode?.duration_seconds ? `${episode.duration_seconds}s` : '未探测'}</span>
              <span>大小：{episode?.file_size ? `${(episode.file_size / 1024 / 1024).toFixed(1)} MB` : '-'}</span>
              <span>创建：{episode?.created_at?.slice(0, 10) ?? '-'}</span>
            </div>
          </div>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <button type="button" className="btn-secondary" onClick={onClose}>
            取消
          </button>
          <button type="submit" className="btn-primary" disabled={saveMut.isPending}>
            {saveMut.isPending ? '保存中...' : '保存'}
          </button>
        </div>
      </form>

      {browsing && (
        <PathBrowser
          open
          selectMode="file"
          // Common video container extensions; folders still show for descent.
          acceptExt={['.mp4', '.mkv', '.mov', '.avi', '.flv', '.webm', '.m4v', '.ts', '.wmv']}
          title="选择视频文件"
          initialPath={path ? path.slice(0, path.lastIndexOf('/')) : '/'}
          onClose={() => setBrowsing(false)}
          onPick={(p) => {
            setPath(p);
            setBrowsing(false);
          }}
        />
      )}
    </Modal>
  );
}
