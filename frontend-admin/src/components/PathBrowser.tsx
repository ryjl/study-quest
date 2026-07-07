import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { formatFileSize } from '../lib/format';
import { Modal, LoadingState } from './ui';

// PathBrowser — a click-through picker for AList/WebDAV paths.
// Walks one directory level at a time via api.scanPath (non-recursive ListDir).
// - selectMode='dir' (default): pick a FOLDER; descending happens on click.
// - selectMode='file': pick a single FILE; folders still descend on click, the
//   selected file is highlighted and confirmed via the primary button. Pass
//   acceptExt to grey out non-matching files (e.g. video extensions).
export function PathBrowser({
  open,
  initialPath,
  selectMode = 'dir',
  acceptExt,
  title,
  onClose,
  onPick,
}: {
  open: boolean;
  initialPath: string;
  selectMode?: 'dir' | 'file';
  // When set (e.g. ['.mp4','.mkv']), non-folder entries with other extensions
  // are dimmed and not selectable. Omit to allow any file.
  acceptExt?: string[];
  title?: string;
  onClose: () => void;
  onPick: (path: string) => void;
}) {
  const [cwd, setCwd] = useState(initialPath || '/');
  const [selected, setSelected] = useState<string>('');

  useEffect(() => {
    if (open) {
      setCwd(initialPath || '/');
      setSelected('');
    }
  }, [open, initialPath]);

  const listQ = useQuery({
    queryKey: ['browse', cwd],
    queryFn: () => api.scanPath(cwd || '/'),
    enabled: open,
    staleTime: 5_000,
  });

  // Normalize an extension to lowercase ".ext" for comparison.
  const extOf = (name: string): string => {
    const dot = name.lastIndexOf('.');
    return dot >= 0 ? name.slice(dot).toLowerCase() : '';
  };
  const isAccepted = (name: string): boolean => {
    if (!acceptExt || acceptExt.length === 0) return true;
    return acceptExt.includes(extOf(name));
  };

  const entries = (listQ.data ?? [])
    .filter((e) => {
      // In file mode, still show folders (to descend) but hide non-matching
      // files when acceptExt is configured.
      if (e.is_dir) return true;
      if (selectMode === 'file' && acceptExt && acceptExt.length > 0) {
        return isAccepted(e.name);
      }
      return true;
    })
    .slice()
    .sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
      return a.name.localeCompare(b.name, 'zh');
    });

  const segments = cwd.split('/').filter(Boolean);

  const descend = (name: string) => {
    const base = cwd.endsWith('/') ? cwd : cwd + '/';
    setCwd(base + name);
    setSelected('');
  };

  const onEntryClick = (isDir: boolean, name: string, path: string) => {
    if (isDir) {
      descend(name);
    } else if (selectMode === 'file') {
      setSelected(path);
    }
  };

  const confirmLabel = selectMode === 'file' ? '选择此文件' : '选择此目录';
  const confirmValue = selectMode === 'file' ? selected : cwd;
  const canConfirm = selectMode === 'file' ? selected !== '' : true;

  const headerTitle = title ?? (selectMode === 'file' ? '选择文件' : '浏览目录');

  return (
    <Modal open={open} onClose={onClose} title={headerTitle} size="lg">
      {/* Breadcrumb */}
      <div className="mb-3 flex flex-wrap items-center gap-1 rounded-lg border border-border bg-card-2 px-3 py-2 text-sm">
        <button className="text-muted hover:text-primary" onClick={() => { setCwd('/'); setSelected(''); }} title="根目录">
          🏠 /
        </button>
        {segments.map((seg, i) => {
          const sub = '/' + segments.slice(0, i + 1).join('/');
          const last = i === segments.length - 1;
          return (
            <span key={sub} className="flex items-center gap-1">
              <span className="text-muted">/</span>
              <button
                className={last ? 'font-semibold text-txt' : 'text-muted hover:text-primary'}
                onClick={() => { setCwd(sub); setSelected(''); }}
              >
                {seg}
              </button>
            </span>
          );
        })}
      </div>

      {/* Entry list */}
      <div className="max-h-[50vh] overflow-y-auto rounded-lg border border-border">
        {listQ.isLoading ? (
          <LoadingState />
        ) : listQ.error ? (
          <div className="p-6 text-center text-sm text-bad">
            读取目录失败：{(listQ.error as Error).message}
          </div>
        ) : entries.length === 0 ? (
          <div className="p-6 text-center text-sm text-muted">
            {acceptExt ? '该目录下没有匹配类型的文件。' : '该目录为空。'}
          </div>
        ) : (
          <ul className="divide-y divide-border">
            {entries.map((e) => {
              const isSel = selected === e.path;
              const dim = !e.is_dir && !isAccepted(e.name);
              return (
                <li key={e.path}>
                  <button
                    type="button"
                    className={`flex w-full items-center gap-3 px-3 py-2 text-left text-sm transition ${
                      isSel ? 'bg-primary/10 ring-1 ring-inset ring-primary/40' : 'hover:bg-card-2'
                    } ${dim ? 'opacity-40' : ''}`}
                    onDoubleClick={() => e.is_dir && descend(e.name)}
                    onClick={() => onEntryClick(e.is_dir, e.name, e.path)}
                    disabled={dim}
                  >
                    <span className="text-base">{e.is_dir ? '📁' : '🎬'}</span>
                    <span className={`flex-1 truncate ${e.is_dir ? 'font-medium text-txt' : 'text-txt'}`}>
                      {e.name}
                    </span>
                    {!e.is_dir && e.size > 0 && (
                      <span className="flex-shrink-0 text-xs text-muted">{formatFileSize(e.size)}</span>
                    )}
                    {e.is_dir && <span className="flex-shrink-0 text-xs text-muted">→</span>}
                    {!e.is_dir && isSel && <span className="flex-shrink-0 text-xs text-primary">✓ 已选</span>}
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      {/* Actions */}
      <div className="mt-4 flex items-center justify-between gap-2">
        <code className="truncate rounded bg-card-2 px-2 py-1 text-xs text-muted" title={selectMode === 'file' ? selected : cwd}>
          {selectMode === 'file' ? (selected || '尚未选择文件') : cwd}
        </code>
        <div className="flex flex-shrink-0 gap-2">
          <button className="btn-secondary" onClick={onClose}>
            取消
          </button>
          <button
            className="btn-primary"
            onClick={() => canConfirm && onPick(confirmValue)}
            disabled={listQ.isLoading || !canConfirm}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
      <p className="mt-2 text-xs text-muted">
        提示：单击文件夹进入下一级；
        {selectMode === 'file' ? '单击文件选中，再点「选择此文件」确认。' : ''}
      </p>
    </Modal>
  );
}
