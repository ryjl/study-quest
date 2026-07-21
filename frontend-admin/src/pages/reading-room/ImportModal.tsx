// Folder Import Modal — pick an Alist folder, preview the PDF tree, and create
// a ReadingSeries + ReadingBook rows. Mirrors the video Import.tsx wizard but
// simplified. The tree nodes carry a per-node `type` that the admin toggles
// (book / series / pass-through / exclude); the recursive toggle cascades to
// children.

import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Check, CheckCircle2, Folder, FolderOpen, BookMarked, XCircle } from 'lucide-react';
import { api } from '../../lib/api';
import { Modal } from '../../components/ui';
import { GradePicker } from '../../components/inputs';
import { PathBrowser } from '../../components/PathBrowser';
import { useToast } from '../../lib/toast';
import { useSubjects } from '../../lib/useSubjects';

export interface ReadingImportNode {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  hash: string;
  type: string; // series | book | pass-through | exclude
  children?: ReadingImportNode[];
}

export function ReadingImportModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const toast = useToast();
  const subjectsQ = useSubjects();
  const [path, setPath] = useState('/');
  const [browsing, setBrowsing] = useState(false);
  const [tree, setTree] = useState<ReadingImportNode | null>(null);
  const [newTitle, setNewTitle] = useState('');
  const [newGrade, setNewGrade] = useState('universal');
  const [newSubject, setNewSubject] = useState('');

  useEffect(() => {
    if (subjectsQ.data && subjectsQ.data.length > 0 && !newSubject) {
      setNewSubject(subjectsQ.data[0].key);
    }
  }, [subjectsQ.data, newSubject]);

  const previewMut = useMutation({
    mutationFn: (scanPath: string) => api.previewReadingImport(scanPath || path),
    onSuccess: (data) => {
      const t = data as ReadingImportNode;
      setTree(t);
      if (t && !newTitle) setNewTitle(t.name);
    },
    onError: (e: unknown) => toast.error((e as Error).message ?? '扫描失败'),
  });

  const executeMut = useMutation({
    mutationFn: async () => {
      if (!tree) throw new Error('请先扫描文件夹');
      if (!newTitle.trim()) throw new Error('请填写系列名称');
      const body = {
        tree,
        new_series: {
          title: newTitle.trim(),
          grade: newGrade || 'universal',
          subject: newSubject,
          cover_url: '',
          tag_ids: [] as number[],
        },
      };
      return api.executeReadingImport(body);
    },
    onSuccess: () => {
      toast.success('导入成功');
      onDone();
      onClose();
    },
    onError: (e: unknown) => toast.error((e as Error).message ?? '导入失败'),
  });

  const setSubtreeType = (node: ReadingImportNode, type: string) => {
    node.type = type;
    if (node.children) {
      for (const c of node.children)
        setSubtreeType(
          c,
          type === 'exclude'
            ? 'exclude'
            : c.is_dir
              ? c === node.children[0] && node.type === 'series'
                ? 'series'
                : 'pass-through'
              : 'book',
        );
    }
  };

  const toggleNode = (node: ReadingImportNode) => {
    if (node.type === 'exclude') {
      node.type = node.is_dir ? 'pass-through' : 'book';
    } else {
      setSubtreeType(node, 'exclude');
    }
    setTree({ ...tree! });
  };

  const countBooks = (node: ReadingImportNode | null): number => {
    if (!node) return 0;
    if (!node.is_dir) return node.type === 'book' ? 1 : 0;
    return (node.children ?? []).reduce((sum, c) => sum + countBooks(c), 0);
  };

  return (
    <Modal open onClose={onClose} title="从文件夹导入" size="lg">
      <div className="space-y-4">
        {/* Step 1: pick folder */}
        <div>
          <label className="mb-1 block text-xs text-muted">网盘文件夹路径</label>
          <div className="flex gap-2">
            <input
              className="input"
              placeholder="/books/恐龙系列"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              spellCheck={false}
            />
            <button className="btn-secondary whitespace-nowrap" onClick={() => setBrowsing(true)}>
              浏览...
            </button>
            <button
              className="btn-primary whitespace-nowrap"
              onClick={() => previewMut.mutate(path)}
              disabled={previewMut.isPending}
            >
              {previewMut.isPending ? '扫描中...' : '扫描'}
            </button>
          </div>
          <p className="mt-1 text-xs text-muted">文件夹名会自动填入系列名称。文件夹内的 PDF 文件会变成书籍。</p>
        </div>

        {/* Step 2: series fields + preview */}
        {tree && (
          <>
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="mb-1 block text-xs text-muted">系列名称</label>
                <input className="input" value={newTitle} onChange={(e) => setNewTitle(e.target.value)} />
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">科目</label>
                <select className="input" value={newSubject} onChange={(e) => setNewSubject(e.target.value)}>
                  {(subjectsQ.data ?? []).map((s) => (
                    <option key={s.key} value={s.key}>
                      {s.label}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            <div>
              <label className="mb-1 block text-xs text-muted">年级</label>
              <GradePicker value={newGrade} onChange={setNewGrade} />
            </div>

            {/* Preview tree */}
            <div>
              <div className="mb-2 flex items-center justify-between">
                <label className="text-xs text-muted">预览（点击切换 包含/排除）</label>
                <span className="text-xs text-muted">将导入 {countBooks(tree)} 本书</span>
              </div>
              <div className="max-h-60 overflow-auto rounded-lg border border-border bg-card-2 p-3">
                <ReadingImportTree node={tree} onToggle={toggleNode} />
              </div>
            </div>

            <button
              className="btn-primary inline-flex w-full items-center justify-center gap-1.5"
              onClick={() => executeMut.mutate()}
              disabled={executeMut.isPending}
            >
              {executeMut.isPending ? (
                '导入中...'
              ) : (
                <>
                  <Check size={14} /> 确认导入 {countBooks(tree)} 本书
                </>
              )}
            </button>
          </>
        )}
      </div>

      {browsing && (
        <PathBrowser
          open
          initialPath={path}
          selectMode="dir"
          onClose={() => setBrowsing(false)}
          onPick={(p) => {
            setPath(p);
            setBrowsing(false);
            previewMut.mutate(p);
          }}
        />
      )}
    </Modal>
  );
}

function ReadingImportTree({
  node,
  onToggle,
  depth = 0,
}: {
  node: ReadingImportNode;
  onToggle: (n: ReadingImportNode) => void;
  depth?: number;
}) {
  const excluded = node.type === 'exclude';
  return (
    <div style={{ marginLeft: depth > 0 ? 16 : 0 }}>
      <button
        type="button"
        onClick={() => onToggle(node)}
        className={`flex w-full items-center gap-2 rounded px-2 py-1 text-left text-sm transition hover:bg-card ${
          excluded ? 'opacity-40' : ''
        }`}
      >
        <span className="text-muted">
          {node.is_dir ? (depth === 0 ? <Folder size={14} /> : <FolderOpen size={14} />) : <BookMarked size={14} />}
        </span>
        <span className="flex-1 truncate" style={{ textDecoration: excluded ? 'line-through' : 'none' }}>
          {node.name}
        </span>
        {!node.is_dir && node.size > 0 && (
          <span className="text-xs text-muted">{(node.size / 1024 / 1024).toFixed(1)} MB</span>
        )}
        <span className={`${excluded ? 'text-bad' : 'text-good'}`}>
          {excluded ? <XCircle size={14} /> : <CheckCircle2 size={14} />}
        </span>
      </button>
      {node.children && node.children.length > 0 && (
        <div>
          {node.children.map((child, i) => (
            <ReadingImportTree key={i} node={child} onToggle={onToggle} depth={depth + 1} />
          ))}
        </div>
      )}
    </div>
  );
}
