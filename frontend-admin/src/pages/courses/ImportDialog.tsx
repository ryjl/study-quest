import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Folder, FolderInput, Film, Check, BookOpen } from 'lucide-react';
import { api } from '../../lib/api';
import type { ImportPreviewNode } from '../../lib/types';
import { EmptyState, Modal, Tag } from '../../components/ui';
import { PathBrowser } from '../../components/PathBrowser';
import { useSubjects } from '../../lib/useSubjects';
import { useStorageSources } from '../../lib/useStorageSources';
import { GradePicker, ImageUpload } from '../../components/inputs';
import { TagInput } from '../../components/TagInput';
import { useToast } from '../../lib/toast';
import { formatFileSize } from '../../lib/format';

type ImportMode = 'existing' | 'new';

// Originally a standalone page (src/pages/Import.tsx); migrated into a dialog
// triggered from the Courses page header. The 3-step wizard logic (source/dir
// select → preview tree → config + execute) is unchanged — only the shell moved
// from a PageHeader page into a Modal(size=xl).
export function ImportDialog({ open, onClose, onImported }: { open: boolean; onClose: () => void; onImported: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];
  const sourcesQ = useStorageSources();
  const sources = sourcesQ.data ?? [];

  // Step state
  const [path, setPath] = useState('/');
  const [sourceId, setSourceId] = useState<number | undefined>(undefined);
  const [browsing, setBrowsing] = useState(false);
  const [mode, setMode] = useState<ImportMode>('new');
  const [targetCourseId, setTargetCourseId] = useState(0);

  // New course fields
  const [newTitle, setNewTitle] = useState('');
  const [newGrade, setNewGrade] = useState('');
  const [newSubject, setNewSubject] = useState('');
  const [newContentType, setNewContentType] = useState<'learning' | 'entertainment'>('learning');
  const [newCover, setNewCover] = useState('');
  const [newTagIDs, setNewTagIDs] = useState<number[]>([]);

  // Reset all wizard state when the dialog closes so the next open starts fresh.
  // (Keeps business logic intact — only augments the dialog shell behavior.)
  const prevOpen = useRef(open);
  useEffect(() => {
    if (prevOpen.current && !open) {
      setPath('/');
      setBrowsing(false);
      setMode('new');
      setTargetCourseId(0);
      setNewTitle('');
      setNewGrade('');
      setNewSubject('');
      setNewContentType('learning');
      setNewCover('');
      setNewTagIDs([]);
      setTree(null);
    }
    prevOpen.current = open;
  }, [open]);

  // Default the subject select once the catalog loads OR when the admin
  // switches content type. The latter matters because the subject dropdown is
  // filtered by content type (academic vs entertainment) — if we didn't
  // re-default here, switching 学习→娱乐 would leave newSubject pointing at
  // an academic subject that's no longer in the filtered dropdown, producing
  // an empty <select> value that silently submits the wrong subject. Pick the
  // first subject of the matching category; if none exists, clear it so the
  // admin sees the gap (rather than submitting a hidden stale value).
  useEffect(() => {
    if (subjects.length === 0) return;
    const wantCategory = newContentType === 'entertainment' ? 'entertainment' : 'academic';
    const match = subjects.find((s) => s.category === wantCategory) ??
                  subjects.find((s) => !s.category && wantCategory === 'academic') ??
                  subjects[0];
    // Only overwrite if the current subject doesn't fit the new filter —
    // avoids clobbering an admin's manual selection when the catalog refreshes.
    const current = subjects.find((s) => s.key === newSubject);
    const currentFits =
      current &&
      (newContentType === 'entertainment'
        ? current.category === 'entertainment'
        : current.category === 'academic' || !current.category);
    if (!currentFits) setNewSubject(match.key);
  }, [subjects, newContentType, newSubject]);

  // Default sourceId to the default source (or the first one) once the catalog
  // loads, so the very first scan picks a real source instead of the global
  // fallback. If no sources are configured, sourceId stays undefined and the
  // backend falls back to the global storage_* settings (legacy behavior).
  // Also reset to undefined if the selected source is deleted mid-session, so
  // the next effect re-picks a live one instead of scanning a dead id.
  useEffect(() => {
    if (sources.length === 0) {
      if (sourceId !== undefined) setSourceId(undefined);
      return;
    }
    if (sourceId !== undefined && !sources.some((s) => s.id === sourceId)) {
      setSourceId(undefined);
      return;
    }
    if (sourceId === undefined) {
      const def = sources.find((s) => s.is_default) ?? sources[0];
      setSourceId(def.id);
    }
  }, [sources, sourceId]);

  const [tree, setTree] = useState<ImportPreviewNode | null>(null);

  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });

  const previewMut = useMutation({
    mutationFn: (scanPath: string) => api.previewTree(scanPath || path, sourceId),
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
      const body: Record<string, unknown> = { tree: serializeTree(tree), source_id: sourceId };
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
      // Notify parent (Courses page) to refresh, then close the dialog.
      onImported();
      onClose();
    },
    onError: (e) => toast.error('导入失败: ' + (e as Error).message),
  });

  return (
    <Modal open={open} onClose={onClose} title="文件导入" size="xl">
      {/* Step 1: Path */}
      <div className="card mb-5">
        <div className="mb-3 flex items-center gap-2">
          <span className="flex h-6 w-6 items-center justify-center rounded-full bg-primary text-xs font-bold text-white">1</span>
          <h2 className="font-semibold text-txt">选择扫描路径</h2>
        </div>
        {sources.length > 0 && (
          <div className="mb-3">
            <label className="mb-1 block text-xs text-muted">存储源</label>
            <select
              className="input"
              value={sourceId ?? ''}
              onChange={(e) => {
                const v = e.target.value;
                setSourceId(v === '' ? undefined : Number(v));
                setTree(null); // switching source invalidates the prior preview
              }}
            >
              {sources.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}{s.is_default ? '（默认）' : ''} — {s.type}
                </option>
              ))}
            </select>
          </div>
        )}
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
          <button className="btn-primary inline-flex items-center gap-1.5 whitespace-nowrap" onClick={() => setBrowsing(true)} title="浏览目录并扫描" disabled={previewMut.isPending}>
            {previewMut.isPending ? '扫描中...' : <><Folder size={14} /> 浏览目录</>}
          </button>
        </div>
      </div>

      {browsing && (
        <PathBrowser
          open
          initialPath={path}
          sourceId={sourceId}
          onClose={() => setBrowsing(false)}
          onPick={(p) => {
            setPath(p);
            setBrowsing(false);
            // Auto-scan immediately after picking a folder — the separate
            // "开始扫描" button exists only for the manual-path entry case.
            // sourceId is captured from render scope by previewMut.mutationFn.
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
              {/* Content type toggle: 学习 / 娱乐. Filters the 科目 dropdown below
                  the same way CourseModal does, so the admin picks a subject of
                  the matching category. The backend derives Course.ContentType
                  from the chosen subject's Category (import_service.go), so we
                  don't send content_type explicitly — getting the subject right
                  is what matters. Pre-2026-07-21 this form showed ALL subjects
                  unfiltered, mixing 动画片/电影 (entertainment) with 数学/语文
                  (academic) in one list — confusing and error-prone. */}
              <div>
                <label className="mb-1 block text-xs text-muted">内容类型</label>
                <div className="flex gap-2">
                  <button
                    type="button"
                    onClick={() => setNewContentType('learning')}
                    className={`flex flex-1 items-center justify-center gap-1.5 rounded-md border px-3 py-2 text-sm transition-colors ${newContentType === 'learning' ? 'border-txt bg-card-2 text-txt font-medium' : 'border-border text-muted hover:text-txt'}`}
                  >
                    <BookOpen size={14} /> 学习
                  </button>
                  <button
                    type="button"
                    onClick={() => setNewContentType('entertainment')}
                    className={`flex flex-1 items-center justify-center gap-1.5 rounded-md border px-3 py-2 text-sm transition-colors ${newContentType === 'entertainment' ? 'border-txt bg-card-2 text-txt font-medium' : 'border-border text-muted hover:text-txt'}`}
                  >
                    <Film size={14} /> 娱乐
                  </button>
                </div>
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">
                  {newContentType === 'entertainment' ? '娱乐分类' : '类别 / 科目'}
                </label>
                <select className="input" value={newSubject} onChange={(e) => setNewSubject(e.target.value)}>
                  {subjects
                    .filter((s) => {
                      // 学习: academic (or unlabeled legacy — default academic).
                      // 娱乐: entertainment only. Same filter rule as CourseModal.
                      if (newContentType === 'entertainment') return s.category === 'entertainment';
                      return s.category === 'academic' || !s.category;
                    })
                    .map((s) => (
                      <option key={s.key} value={s.key}>
                        {s.label} ({s.key})
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
              <button className="btn-primary btn-sm inline-flex items-center gap-1.5" onClick={() => executeMut.mutate()} disabled={executeMut.isPending}>
                {executeMut.isPending ? '导入中...' : <><Check size={14} /> 确认导入</>}
              </button>
            </div>
          </div>
          <PreviewTree node={tree} onChange={setTree} />
        </div>
      )}

      {!tree && !previewMut.isPending && <EmptyState icon={<FolderInput size={28} />} title="未扫描" hint="点击「浏览目录」选择网盘文件夹后自动扫描" />}
    </Modal>
  );
}

const TYPE_COLORS: Record<string, string> = {
  course: '#6366f1',
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
        <span className="text-muted">{isDir ? <Folder size={14} /> : <Film size={14} />}</span>
        {node.type === 'course' ? (
          <span className="font-bold text-txt flex-1 py-1 text-sm px-2 select-none" title="课程库名称以步骤 2 中填写的为准">{node.name}</span>
        ) : (
          <input
            className="input !py-1 !text-sm flex-1 min-w-0"
            value={node.name}
            onChange={(e) => onChange({ ...node, name: e.target.value })}
          />
        )}
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
