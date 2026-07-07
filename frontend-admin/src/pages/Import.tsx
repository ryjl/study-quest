import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { ImportPreviewNode } from '../lib/types';
import { EmptyState, Tag } from '../components/ui';
import { PathBrowser } from '../components/PathBrowser';
import { useSubjects } from '../lib/useSubjects';
import { GradePicker, ImageUpload } from '../components/inputs';
import { TagInput } from '../components/TagInput';
import { useToast } from '../lib/toast';
import { formatFileSize } from '../lib/format';

type ImportMode = 'existing' | 'new';

export function Import() {
  const toast = useToast();
  const qc = useQueryClient();
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];

  // Step state
  const [path, setPath] = useState('/');
  const [browsing, setBrowsing] = useState(false);
  const [mode, setMode] = useState<ImportMode>('new');
  const [targetCourseId, setTargetCourseId] = useState(0);

  // New course fields
  const [newTitle, setNewTitle] = useState('');
  const [newGrade, setNewGrade] = useState('');
  const [newSubject, setNewSubject] = useState('');
  const [newCover, setNewCover] = useState('');
  const [newTagIDs, setNewTagIDs] = useState<number[]>([]);

  // Default the subject select once the catalog loads.
  useEffect(() => {
    if (!newSubject && subjects.length > 0) setNewSubject(subjects[0].key);
  }, [subjects, newSubject]);

  const [tree, setTree] = useState<ImportPreviewNode | null>(null);

  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });

  const previewMut = useMutation({
    mutationFn: (scanPath: string) => api.previewTree(scanPath || path),
    onSuccess: (t) => {
      setTree(t);
      // Auto-suggest new course title from root
      if (!newTitle && t.name) setNewTitle(t.name);
    },
    onError: (e) => toast.error('扫描失败: ' + (e as Error).message),
  });

  const executeMut = useMutation({
    mutationFn: () => {
      if (!tree) throw new Error('请先扫描');
      const body: Record<string, unknown> = { tree: serializeTree(tree) };
      if (mode === 'existing') {
        if (!targetCourseId) throw new Error('请选择目标课程');
        body.target_course_id = targetCourseId;
      } else {
        if (!newTitle.trim()) throw new Error('请输入新课程名称');
        body.new_course = { title: newTitle.trim(), grade: newGrade || 'universal', subject: newSubject, cover_url: newCover, tag_ids: newTagIDs };
      }
      return api.executeImport(body);
    },
    onSuccess: () => {
      toast.success('导入成功！');
      qc.invalidateQueries({ queryKey: ['courses'] });
      setTree(null);
      setNewTitle('');
      setNewGrade('');
    },
    onError: (e) => toast.error('导入失败: ' + (e as Error).message),
  });

  return (
    <div>
      <div className="mb-6 border-b border-border pb-4">
        <h1 className="text-2xl font-bold text-txt">智能导入向导</h1>
        <p className="mt-1 text-sm text-muted">扫描网盘目录 → 预览映射 → 一键导入。系统会自动识别 课程/章节/课时 结构。</p>
      </div>

      {/* Step 1: Path */}
      <div className="card mb-5">
        <div className="mb-3 flex items-center gap-2">
          <span className="flex h-6 w-6 items-center justify-center rounded-full bg-primary text-xs font-bold text-white">1</span>
          <h2 className="font-semibold text-txt">选择扫描路径</h2>
        </div>
        <div className="flex gap-2">
          <input
            className="input font-mono"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && path && !previewMut.isPending) previewMut.mutate(path);
            }}
            placeholder="/Physics"
          />
          <button className="btn-primary whitespace-nowrap" onClick={() => setBrowsing(true)} title="浏览目录并扫描" disabled={previewMut.isPending}>
            {previewMut.isPending ? '扫描中...' : '📁 浏览目录'}
          </button>
        </div>
      </div>

      {browsing && (
        <PathBrowser
          open
          initialPath={path}
          onClose={() => setBrowsing(false)}
          onPick={(p) => {
            setPath(p);
            setBrowsing(false);
            // Auto-scan immediately after picking a folder — the separate
            // "开始扫描" button exists only for the manual-path entry case.
            previewMut.mutate(p);
          }}
        />
      )}

      {/* Step 2: Config */}
      {tree && (
        <div className="card mb-5">
          <div className="mb-3 flex items-center gap-2">
            <span className="flex h-6 w-6 items-center justify-center rounded-full bg-primary text-xs font-bold text-white">2</span>
            <h2 className="font-semibold text-txt">导入目标</h2>
          </div>
          <div className="mb-4 flex gap-3">
            <button className={mode === 'new' ? 'btn-primary' : 'btn-secondary'} onClick={() => setMode('new')}>
              新建课程
            </button>
            <button className={mode === 'existing' ? 'btn-primary' : 'btn-secondary'} onClick={() => setMode('existing')}>
              导入到已有课程
            </button>
          </div>

          {mode === 'existing' ? (
            <select className="input max-w-md" value={targetCourseId} onChange={(e) => setTargetCourseId(Number(e.target.value))}>
              <option value={0}>选择目标课程...</option>
              {coursesQ.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.title}
                </option>
              ))}
            </select>
          ) : (
            <div className="grid max-w-2xl gap-3">
              <div>
                <label className="mb-1 block text-xs text-muted">课程名称</label>
                <input className="input" value={newTitle} onChange={(e) => setNewTitle(e.target.value)} required />
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">适用年级</label>
                <GradePicker value={newGrade} onChange={setNewGrade} />
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">科目</label>
                <select className="input" value={newSubject} onChange={(e) => setNewSubject(e.target.value)}>
                  {subjects.map((s) => (
                    <option key={s.key} value={s.key}>
                      {s.emoji} {s.label}
                    </option>
                  ))}
                </select>
              </div>
              <ImageUpload label="封面" value={newCover} onChange={setNewCover} />
              <div>
                <label className="mb-1 block text-xs text-muted">标签</label>
                <TagInput value={newTagIDs} onChange={setNewTagIDs} />
              </div>
            </div>
          )}
        </div>
      )}

      {/* Step 3: Preview tree */}
      {tree && (
        <div className="card mb-5">
          <div className="mb-3 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="flex h-6 w-6 items-center justify-center rounded-full bg-primary text-xs font-bold text-white">3</span>
              <h2 className="font-semibold text-txt">预览与确认</h2>
            </div>
            <div className="flex gap-2">
              <button className="btn-danger btn-sm" onClick={() => setTree(null)}>
                取消
              </button>
              <button className="btn-primary btn-sm" onClick={() => executeMut.mutate()} disabled={executeMut.isPending}>
                {executeMut.isPending ? '导入中...' : '✓ 确认导入'}
              </button>
            </div>
          </div>
          <PreviewTree node={tree} onChange={setTree} />
        </div>
      )}

      {!tree && !previewMut.isPending && <EmptyState icon="📥" title="未扫描" hint="点击「浏览目录」选择网盘文件夹后自动扫描" />}
    </div>
  );
}

const TYPE_COLORS: Record<string, string> = {
  course: '#a78bfa',
  chapter: '#60a5fa',
  episode: '#34d399',
  'pass-through': '#9ca3af',
  exclude: '#ef4444',
};

function PreviewTree({ node, onChange, depth = 0 }: { node: ImportPreviewNode; onChange: (n: ImportPreviewNode) => void; depth?: number }) {
  const setType = (type: string) => {
    let next = { ...node, type };
    // Excluding a folder cascades to all descendants so the user sees (and the
    // backend receives) the whole subtree as skipped. Re-enabling a folder
    // does NOT auto-restore children — the user re-decides per child.
    if (type === 'exclude' && node.children && node.children.length > 0) {
      next = { ...next, children: node.children.map((c) => setSubtreeType(c, 'exclude')) };
    }
    onChange(next);
  };

  const updateChild = (idx: number, child: ImportPreviewNode) => {
    const children = [...(node.children ?? [])];
    children[idx] = child;
    onChange({ ...node, children });
  };

  const isDir = node.is_dir;
  return (
    <div style={{ marginLeft: depth > 0 ? 16 : 0 }}>
      <div
        className="mb-1 flex items-center gap-2 rounded-lg border border-border bg-card-2 px-3 py-2"
        style={{ borderLeftColor: TYPE_COLORS[node.type], borderLeftWidth: 3 }}
      >
        <span>{isDir ? '📁' : '🎬'}</span>
        <input
          className="input !py-1 !text-sm flex-1 min-w-0"
          value={node.name}
          onChange={(e) => onChange({ ...node, name: e.target.value })}
        />
        <select className="input !py-1 !text-xs max-w-[130px]" value={node.type} onChange={(e) => setType(e.target.value)}>
          {isDir ? (
            <>
              <option value="course">→ 课程</option>
              <option value="chapter">→ 章节</option>
              <option value="pass-through">→ 穿透</option>
              <option value="exclude">✕ 跳过</option>
            </>
          ) : (
            <>
              <option value="episode">→ 课时</option>
              <option value="exclude">✕ 跳过</option>
            </>
          )}
        </select>
        {!isDir && <span className="text-xs text-muted">{formatFileSize(node.size)}</span>}
        {node.hash && <Tag color="#10b981">hash</Tag>}
      </div>
      {node.children && node.children.length > 0 && (
        <div className="ml-2 border-l border-dashed border-border pl-3">
          {node.children.map((c, i) => (
            <PreviewTree key={i} node={c} onChange={(n) => updateChild(i, n)} depth={depth + 1} />
          ))}
        </div>
      )}
    </div>
  );
}

// Recursively sets the type of a node and all its descendants. Used when a
// folder is marked "exclude" so the whole subtree is skipped together.
function setSubtreeType(node: ImportPreviewNode, type: string): ImportPreviewNode {
  return {
    ...node,
    type,
    children: node.children?.map((c) => setSubtreeType(c, type)),
  };
}

function serializeTree(node: ImportPreviewNode): ImportPreviewNode {
  return {
    name: node.name,
    path: node.path,
    is_dir: node.is_dir,
    size: node.size,
    hash: node.hash,
    type: node.type,
    children: node.children?.map(serializeTree),
  };
}
