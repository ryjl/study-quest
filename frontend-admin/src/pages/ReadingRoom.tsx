import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { useSubjects } from '../lib/useSubjects';
import { useToast, useConfirm } from '../lib/toast';
import { Modal, LoadingState, EmptyState } from '../components/ui';
import { ImageUpload, GradePicker } from '../components/inputs';
import { TagInput } from '../components/TagInput';
import { PathBrowser } from '../components/PathBrowser';
import { subjectMeta, gradeDisplay } from '../lib/types';
import type { ReadingSeries, ReadingBook, ReadingArticle } from '../lib/types';
import { PageHeader } from '../components/PageHeader';

// Parse the stored whitelist_domains (JSON array string or comma-separated)
// into a clean comma-separated string for the text input.
function parseWhitelistForDisplay(raw: string): string {
  if (!raw || raw === '[]') return '';
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return parsed.filter((d) => typeof d === 'string' && d).join(', ');
    }
  } catch {
    // Not JSON — treat as already comma-separated.
  }
  return raw;
}

// Reading Room admin page — manages series (container), PDF books, and web
// articles. Mirrors the Courses page structure (filter bar + collapsible series
// cards + standalone section) but simpler (no chapter tree, no probe worker).

export function ReadingRoom() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const subjectsQ = useSubjects();

  const seriesQ = useQuery({ queryKey: ['reading-series'], queryFn: api.listReadingSeries });
  const booksQ = useQuery({ queryKey: ['reading-books'], queryFn: api.listReadingBooks });
  const articlesQ = useQuery({ queryKey: ['reading-articles'], queryFn: api.listReadingArticles });

  const invalidateAll = () => {
    qc.invalidateQueries({ queryKey: ['reading-series'] });
    qc.invalidateQueries({ queryKey: ['reading-books'] });
    qc.invalidateQueries({ queryKey: ['reading-articles'] });
  };

  // --- modals ---
  const [editingSeries, setEditingSeries] = useState<ReadingSeries | null>(null);
  const [creatingSeries, setCreatingSeries] = useState(false);
  const [editingBook, setEditingBook] = useState<ReadingBook | null>(null);
  const [creatingBook, setCreatingBook] = useState(false);
  const [editingArticle, setEditingArticle] = useState<ReadingArticle | null>(null);
  const [creatingArticle, setCreatingArticle] = useState(false);
  const [importing, setImporting] = useState(false);
  const [expandedSeries, setExpandedSeries] = useState<Set<number>>(new Set());

  // --- delete mutations ---
  const delSeriesMut = useMutation({
    mutationFn: api.deleteReadingSeries,
    onSuccess: () => { toast.success('系列已删除'); invalidateAll(); },
    onError: (e: unknown) => toast.error((e as Error).message ?? '删除失败'),
  });
  const delBookMut = useMutation({
    mutationFn: api.deleteReadingBook,
    onSuccess: () => { toast.success('书籍已删除'); invalidateAll(); },
    onError: (e: unknown) => toast.error((e as Error).message ?? '删除失败'),
  });
  const delArticleMut = useMutation({
    mutationFn: api.deleteReadingArticle,
    onSuccess: () => { toast.success('文章已删除'); invalidateAll(); },
    onError: (e: unknown) => toast.error((e as Error).message ?? '删除失败'),
  });

  // --- filter ---
  const [search, setSearch] = useState('');
  const [subjectFilter, setSubjectFilter] = useState('');

  const series = seriesQ.data ?? [];
  const standaloneBooks = (booksQ.data ?? []).filter((b) => b.series_id === 0);
  const standaloneArticles = (articlesQ.data ?? []).filter((a) => a.series_id === 0);

  const matches = (s: { title: string; subject: string }) => {
    if (subjectFilter && s.subject !== subjectFilter) return false;
    if (search && !s.title.toLowerCase().includes(search.toLowerCase())) return false;
    return true;
  };

  const filteredSeries = series.filter(matches);
  const filteredBooks = standaloneBooks.filter(matches);
  const filteredArticles = standaloneArticles.filter(matches);

  if (seriesQ.isLoading) return <LoadingState />;

  return (
    <div>
      <PageHeader
        title="阅读室"
        breadcrumb={[{ label: '内容运营' }]}
        description="管理阅读系列、PDF 书籍与文章。"
        actions={
          <div className="flex gap-2">
            <button className="btn-secondary" onClick={() => setImporting(true)}>📥 从文件夹导入</button>
            <button className="btn-secondary" onClick={() => setCreatingBook(true)}>+ 添加书籍</button>
            <button className="btn-secondary" onClick={() => setCreatingArticle(true)}>+ 添加文章</button>
            <button className="btn-primary" onClick={() => setCreatingSeries(true)}>+ 新建系列</button>
          </div>
        }
      />

      {/* Filter bar */}
      <div className="mb-5 flex flex-wrap items-center gap-3 rounded-xl border border-border bg-card p-3">
        <input
          className="input max-w-xs"
          placeholder="搜索标题..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <select className="input max-w-[180px]" value={subjectFilter} onChange={(e) => setSubjectFilter(e.target.value)}>
          <option value="">全部科目</option>
          {(subjectsQ.data ?? []).map((s) => (
            <option key={s.key} value={s.key}>{s.emoji} {s.label}</option>
          ))}
        </select>
        <span className="text-sm text-muted">
          {filteredSeries.length} 系列 · {filteredBooks.length} 散本 · {filteredArticles.length} 散文
        </span>
      </div>

      {series.length === 0 && standaloneBooks.length === 0 && standaloneArticles.length === 0 && (
        <EmptyState
          icon="📖"
          title="阅读室还是空的"
          hint="添加 PDF 绘本/试卷或网页文章，或新建一个系列来组织同主题内容。"
        />
      )}

      {/* Series section */}
      {filteredSeries.length > 0 && (
        <div className="mb-6">
          <h2 className="mb-3 text-sm font-semibold text-muted">📚 系列</h2>
          <div className="space-y-3">
            {filteredSeries.map((s) => {
              const isOpen = expandedSeries.has(s.id);
              const subj = subjectMeta(s.subject);
              return (
                <div key={s.id} className="card !p-0 overflow-hidden">
                  <div className="flex items-center gap-3 p-4">
                    {s.cover_url ? (
                      <img src={s.cover_url} alt="" className="h-14 w-14 rounded-lg object-cover" />
                    ) : (
                      <div className="flex h-14 w-14 items-center justify-center rounded-lg bg-card-2 text-2xl">{subj.emoji}</div>
                    )}
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-txt">{s.title}</span>
                        <span className="rounded-md px-1.5 py-0.5 text-xs font-medium" style={{ backgroundColor: `${subj.color}20`, color: subj.color }}>{subj.emoji} {subj.label}</span>
                        <span className="text-xs text-muted">{gradeDisplay(s.grade)}</span>
                      </div>
                      <p className="mt-0.5 truncate text-xs text-muted">
                        {s.book_count} 本书 · {s.article_count} 篇文章{s.description ? ` · ${s.description}` : ''}
                      </p>
                    </div>
                    <div className="flex gap-1.5">
                      <button className="btn-ghost btn-sm" onClick={() => {
                        if (isOpen) {
                          setExpandedSeries((prev) => { const n = new Set(prev); n.delete(s.id); return n; });
                        } else {
                          setExpandedSeries((prev) => new Set(prev).add(s.id));
                        }
                      }}>
                        {isOpen ? '收起' : '展开'}
                      </button>
                      <button className="btn-ghost btn-sm" onClick={() => setEditingSeries(s)}>编辑</button>
                      <button className="btn-ghost btn-sm text-bad hover:bg-bad/10" onClick={async () => {
                        if (await confirm({ message: `删除系列「${s.title}」？`, detail: '系列下的书籍/文章会变为散本，不会被删除。', danger: true })) delSeriesMut.mutate(s.id);
                      }} disabled={delSeriesMut.isPending}>删除</button>
                    </div>
                  </div>
                  {isOpen && (
                    <SeriesChildren
                      seriesId={s.id}
                      onEditBook={setEditingBook}
                      onEditArticle={setEditingArticle}
                      onDeleteBook={(b) => delBookMut.mutate(b.id)}
                      onDeleteArticle={(a) => delArticleMut.mutate(a.id)}
                    />
                  )}
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* Standalone books */}
      {filteredBooks.length > 0 && (
        <div className="mb-6">
          <h2 className="mb-3 text-sm font-semibold text-muted">📄 散本（PDF）</h2>
          <div className="space-y-2">
            {filteredBooks.map((b) => {
              const subj = subjectMeta(b.subject);
              return (
                <ReadingItemRow
                  key={b.id}
                  icon="📕"
                  title={b.title}
                  subtitle={`${gradeDisplay(b.grade)} · ${b.page_count ?? '?'}页 · ${b.file_relative_path}`}
                  subjectColor={subj.color}
                  subjectLabel={`${subj.emoji} ${subj.label}`}
                  onEdit={() => setEditingBook(b)}
                  onDelete={async () => {
                    if (await confirm({ message: `删除书籍「${b.title}」？`, detail: '将同时删除该用户的阅读进度记录。', danger: true })) delBookMut.mutate(b.id);
                  }}
                />
              );
            })}
          </div>
        </div>
      )}

      {/* Standalone articles */}
      {filteredArticles.length > 0 && (
        <div className="mb-6">
          <h2 className="mb-3 text-sm font-semibold text-muted">🌐 散文（网页）</h2>
          <div className="space-y-2">
            {filteredArticles.map((a) => {
              const subj = subjectMeta(a.subject);
              return (
                <ReadingItemRow
                  key={a.id}
                  icon="🌐"
                  title={a.title}
                  subtitle={`${gradeDisplay(a.grade)} · ${a.source_url}`}
                  subjectColor={subj.color}
                  subjectLabel={`${subj.emoji} ${subj.label}`}
                  onEdit={() => setEditingArticle(a)}
                  onDelete={async () => {
                    if (await confirm({ message: `删除文章「${a.title}」？`, danger: true })) delArticleMut.mutate(a.id);
                  }}
                />
              );
            })}
          </div>
        </div>
      )}

      {/* Modals */}
      {(creatingSeries || editingSeries) && (
        <SeriesModal series={editingSeries} onClose={() => { setCreatingSeries(false); setEditingSeries(null); }} />
      )}
      {(creatingBook || editingBook) && (
        <BookModal book={editingBook} series={series} onClose={() => { setCreatingBook(false); setEditingBook(null); }} />
      )}
      {(creatingArticle || editingArticle) && (
        <ArticleModal article={editingArticle} series={series} onClose={() => { setCreatingArticle(false); setEditingArticle(null); }} />
      )}
      {importing && (
        <ReadingImportModal onClose={() => setImporting(false)} onDone={invalidateAll} />
      )}
    </div>
  );
}

// --- Series children (expanded view) ---
// Each instance owns its own useQuery keyed to its seriesId, so multiple
// expanded series each load + display their own children correctly.

function SeriesChildren({
  seriesId, onEditBook, onEditArticle, onDeleteBook, onDeleteArticle,
}: {
  seriesId: number;
  onEditBook: (b: ReadingBook) => void;
  onEditArticle: (a: ReadingArticle) => void;
  onDeleteBook: (b: ReadingBook) => void;
  onDeleteArticle: (a: ReadingArticle) => void;
}) {
  const detailQ = useQuery({
    queryKey: ['reading-series-detail', seriesId],
    queryFn: () => api.getReadingSeriesDetail(seriesId),
  });
  if (detailQ.isLoading || !detailQ.data) {
    return <div className="border-t border-border p-4 text-sm text-muted">加载中...</div>;
  }
  const { books, articles } = detailQ.data;
  if (books.length === 0 && articles.length === 0) {
    return <div className="border-t border-border p-4 text-sm text-muted">该系列还没有内容，编辑书籍/文章时可选择归入此系列。</div>;
  }
  return (
    <div className="space-y-1.5 border-t border-border p-3">
      {books.map((b) => (
        <ReadingItemRow
          key={b.id}
          icon="📕"
          title={b.title}
          subtitle={`${b.page_count ?? '?'}页 · ${b.file_relative_path}`}
          compact
          onEdit={() => onEditBook(b)}
          onDelete={() => onDeleteBook(b)}
        />
      ))}
      {articles.map((a) => (
        <ReadingItemRow
          key={a.id}
          icon="🌐"
          title={a.title}
          subtitle={a.source_url}
          compact
          onEdit={() => onEditArticle(a)}
          onDelete={() => onDeleteArticle(a)}
        />
      ))}
    </div>
  );
}

// --- Generic item row ---

function ReadingItemRow({
  icon, title, subtitle, subjectColor, subjectLabel, compact, onEdit, onDelete,
}: {
  icon: string;
  title: string;
  subtitle: string;
  subjectColor?: string;
  subjectLabel?: string;
  compact?: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <div className={`group flex items-center gap-2.5 rounded-lg border border-border bg-card-2 ${compact ? 'px-3 py-1.5' : 'px-3 py-2'}`}>
      <span className="text-lg">{icon}</span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-txt">{title}</span>
          {subjectLabel && (
            <span className="shrink-0 rounded-md px-1.5 py-0.5 text-xs font-medium" style={{ backgroundColor: `${subjectColor}20`, color: subjectColor }}>{subjectLabel}</span>
          )}
        </div>
        <p className="truncate text-xs text-muted">{subtitle}</p>
      </div>
      <div className="flex shrink-0 gap-1 opacity-60 transition group-hover:opacity-100">
        <button className="btn-ghost btn-sm" onClick={onEdit}>编辑</button>
        <button className="btn-ghost btn-sm text-bad hover:bg-bad/10" onClick={onDelete}>删除</button>
      </div>
    </div>
  );
}

// --- Shared form fields ---

function SubjectSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const subjectsQ = useSubjects();
  return (
    <div>
      <label className="mb-1 block text-xs text-muted">科目</label>
      <select className="input" value={value} onChange={(e) => onChange(e.target.value)} required>
        <option value="">请选择科目</option>
        {(subjectsQ.data ?? []).map((s) => (
          <option key={s.key} value={s.key}>{s.emoji} {s.label}</option>
        ))}
      </select>
    </div>
  );
}

function GradeField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-muted">年级（可多选）</label>
      <GradePicker value={value} onChange={onChange} />
    </div>
  );
}

function TagField({ value, onChange }: { value: number[]; onChange: (v: number[]) => void }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-muted">标签</label>
      <TagInput value={value} onChange={onChange} />
    </div>
  );
}

function SeriesSelect({ value, onChange, series }: { value: number; onChange: (v: number) => void; series: ReadingSeries[] }) {
  return (
    <div>
      <label className="mb-1 block text-xs text-muted">所属系列（不选则为散本）</label>
      <select className="input" value={value} onChange={(e) => onChange(Number(e.target.value))}>
        <option value={0}>— 散本/散文（不属于任何系列）—</option>
        {series.map((s) => (
          <option key={s.id} value={s.id}>{s.title}</option>
        ))}
      </select>
    </div>
  );
}

// --- Series Modal ---

function SeriesModal({ series, onClose }: { series: ReadingSeries | null; onClose: () => void }) {
  const isEdit = !!series;
  const toast = useToast();
  const qc = useQueryClient();
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['reading-series'] });
    qc.invalidateQueries({ queryKey: ['reading-series-detail'] });
  };

  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [grade, setGrade] = useState('universal');
  const [subject, setSubject] = useState('');
  const [coverUrl, setCoverUrl] = useState('');
  const [sortOrder, setSortOrder] = useState(0);
  const [tagIds, setTagIds] = useState<number[]>([]);

  useEffect(() => {
    if (series) {
      setTitle(series.title);
      setDescription(series.description);
      setGrade(series.grade || 'universal');
      setSubject(series.subject);
      setCoverUrl(series.cover_url);
      setSortOrder(series.sort_order);
      setTagIds(series.tag_ids ?? []);
    } else {
      setTitle(''); setDescription(''); setGrade('universal'); setSubject('');
      setCoverUrl(''); setSortOrder(0); setTagIds([]);
    }
  }, [series]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = { title: title.trim(), description, grade, subject, cover_url: coverUrl, sort_order: sortOrder, tag_ids: tagIds };
      if (!body.title) throw new Error('请填写标题');
      if (!body.subject) throw new Error('请选择科目');
      if (isEdit && series) return api.updateReadingSeries(series.id, body);
      return api.createReadingSeries(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '系列已更新' : '系列已创建');
      invalidate();
      onClose();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑系列' : '新建系列'} size="md">
      <form onSubmit={(e) => { e.preventDefault(); saveMut.mutate(); }} className="space-y-4">
        <div>
          <label className="mb-1 block text-xs text-muted">标题</label>
          <input className="input" placeholder="如：上博展厅系列" value={title} onChange={(e) => setTitle(e.target.value)} required autoFocus />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">描述</label>
          <textarea className="input" rows={2} placeholder="系列简介（可选）" value={description} onChange={(e) => setDescription(e.target.value)} />
        </div>
        <SubjectSelect value={subject} onChange={setSubject} />
        <GradeField value={grade} onChange={setGrade} />
        <ImageUpload value={coverUrl} onChange={setCoverUrl} />
        <TagField value={tagIds} onChange={setTagIds} />
        <div>
          <label className="mb-1 block text-xs text-muted">排序权重（越小越靠前）</label>
          <input className="input" type="number" value={sortOrder} onChange={(e) => setSortOrder(Number(e.target.value))} />
        </div>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '创建系列'}
        </button>
      </form>
    </Modal>
  );
}

// --- Book Modal (PDF) ---

function BookModal({ book, series, onClose }: { book: ReadingBook | null; series: ReadingSeries[]; onClose: () => void }) {
  const isEdit = !!book;
  const toast = useToast();
  const qc = useQueryClient();
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['reading-books'] });
    qc.invalidateQueries({ queryKey: ['reading-series'] });
    qc.invalidateQueries({ queryKey: ['reading-series-detail'] });
  };

  const [title, setTitle] = useState('');
  const [seriesId, setSeriesId] = useState(0);
  const [grade, setGrade] = useState('universal');
  const [subject, setSubject] = useState('');
  const [fileRelativePath, setFileRelativePath] = useState('');
  const [coverUrl, setCoverUrl] = useState('');
  const [sortOrder, setSortOrder] = useState(0);
  const [tagIds, setTagIds] = useState<number[]>([]);
  const [browsing, setBrowsing] = useState(false);

  useEffect(() => {
    if (book) {
      setTitle(book.title);
      setSeriesId(book.series_id);
      setGrade(book.grade || 'universal');
      setSubject(book.subject);
      setFileRelativePath(book.file_relative_path);
      setCoverUrl(book.cover_url);
      setSortOrder(book.sort_order);
      setTagIds(book.tag_ids ?? []);
    } else {
      setTitle(''); setSeriesId(0); setGrade('universal'); setSubject('');
      setFileRelativePath(''); setCoverUrl(''); setSortOrder(0); setTagIds([]);
    }
  }, [book]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        series_id: seriesId, sort_order: sortOrder, title: title.trim(),
        file_relative_path: fileRelativePath, cover_url: coverUrl,
        grade, subject, tag_ids: tagIds,
      };
      if (!body.title) throw new Error('请填写标题');
      if (!body.file_relative_path) throw new Error('请选择 PDF 文件');
      if (!body.subject) throw new Error('请选择科目');
      if (isEdit && book) return api.updateReadingBook(book.id, body);
      return api.createReadingBook(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '书籍已更新' : '书籍已创建');
      invalidate();
      onClose();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑书籍' : '添加书籍（PDF）'} size="md">
      <form onSubmit={(e) => { e.preventDefault(); saveMut.mutate(); }} className="space-y-4">
        <div>
          <label className="mb-1 block text-xs text-muted">标题</label>
          <input className="input" placeholder="如：恐龙百科" value={title} onChange={(e) => setTitle(e.target.value)} required autoFocus />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">PDF 文件（从网盘选择）</label>
          <div className="flex gap-2">
            <input className="input" placeholder="网盘相对路径" value={fileRelativePath} onChange={(e) => setFileRelativePath(e.target.value)} required spellCheck={false} />
            <button type="button" className="btn-secondary whitespace-nowrap" onClick={() => setBrowsing(true)}>浏览...</button>
          </div>
          <p className="mt-1 text-xs text-muted">首次打开时客户端会下载缓存，之后翻页走本地。</p>
        </div>
        <SubjectSelect value={subject} onChange={setSubject} />
        <GradeField value={grade} onChange={setGrade} />
        <SeriesSelect value={seriesId} onChange={setSeriesId} series={series} />
        <ImageUpload value={coverUrl} onChange={setCoverUrl} />
        <TagField value={tagIds} onChange={setTagIds} />
        <div>
          <label className="mb-1 block text-xs text-muted">排序权重</label>
          <input className="input" type="number" value={sortOrder} onChange={(e) => setSortOrder(Number(e.target.value))} />
        </div>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '添加书籍'}
        </button>
      </form>

      {browsing && (
        <PathBrowser
          open
          initialPath="/"
          selectMode="file"
          acceptExt={['.pdf']}
          title="选择 PDF 文件"
          onClose={() => setBrowsing(false)}
          onPick={(p) => {
            setFileRelativePath(p);
            setBrowsing(false);
          }}
        />
      )}
    </Modal>
  );
}

// --- Article Modal (web) ---

function ArticleModal({ article, series, onClose }: { article: ReadingArticle | null; series: ReadingSeries[]; onClose: () => void }) {
  const isEdit = !!article;
  const toast = useToast();
  const qc = useQueryClient();
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['reading-articles'] });
    qc.invalidateQueries({ queryKey: ['reading-series'] });
    qc.invalidateQueries({ queryKey: ['reading-series-detail'] });
  };

  const [title, setTitle] = useState('');
  const [seriesId, setSeriesId] = useState(0);
  const [grade, setGrade] = useState('universal');
  const [subject, setSubject] = useState('');
  const [sourceUrl, setSourceUrl] = useState('');
  const [whitelistDomains, setWhitelistDomains] = useState('');
  const [coverUrl, setCoverUrl] = useState('');
  const [sortOrder, setSortOrder] = useState(0);
  const [tagIds, setTagIds] = useState<number[]>([]);

  useEffect(() => {
    if (article) {
      setTitle(article.title);
      setSeriesId(article.series_id);
      setGrade(article.grade || 'universal');
      setSubject(article.subject);
      setSourceUrl(article.source_url);
      // Parse the stored whitelist (JSON array or comma string) into a clean
      // comma-separated string for the text input, so the admin sees
      // "mp.weixin.qq.com, mmbiz.qpic.cn" not the raw JSON.
      setWhitelistDomains(parseWhitelistForDisplay(article.whitelist_domains));
      setCoverUrl(article.cover_url);
      setSortOrder(article.sort_order);
      setTagIds(article.tag_ids ?? []);
    } else {
      setTitle(''); setSeriesId(0); setGrade('universal'); setSubject('');
      setSourceUrl(''); setWhitelistDomains(''); setCoverUrl(''); setSortOrder(0); setTagIds([]);
    }
  }, [article]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        series_id: seriesId, sort_order: sortOrder, title: title.trim(),
        source_url: sourceUrl.trim(), whitelist_domains: whitelistDomains.trim(),
        cover_url: coverUrl, grade, subject, tag_ids: tagIds,
      };
      if (!body.title) throw new Error('请填写标题');
      if (!body.source_url) throw new Error('请填写网页 URL');
      if (!body.subject) throw new Error('请选择科目');
      if (isEdit && article) return api.updateReadingArticle(article.id, body);
      return api.createReadingArticle(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '文章已更新' : '文章已创建');
      invalidate();
      onClose();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  const suggestMut = useMutation({
    mutationFn: (url: string) => api.suggestWhitelist(url),
    onSuccess: (data) => {
      const suggested = data.domains.join(', ');
      if (!suggested) {
        toast.info('未从文章中提取到域名');
        return;
      }
      // Append to existing (dedup), don't overwrite manual edits.
      const existing = whitelistDomains.split(',').map((d) => d.trim()).filter(Boolean);
      const merged = [...new Set([...existing, ...data.domains])];
      setWhitelistDomains(merged.join(', '));
      toast.success(`已推荐 ${data.domains.length} 个域名`);
    },
    onError: (e: unknown) => toast.error((e as Error).message ?? '提取失败'),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑文章' : '添加文章（网页）'} size="md">
      <form onSubmit={(e) => { e.preventDefault(); saveMut.mutate(); }} className="space-y-4">
        <div>
          <label className="mb-1 block text-xs text-muted">标题</label>
          <input className="input" placeholder="如：上博展厅导览" value={title} onChange={(e) => setTitle(e.target.value)} required autoFocus />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">网页 URL</label>
          <div className="flex gap-2">
            <input className="input" placeholder="https://mp.weixin.qq.com/..." value={sourceUrl} onChange={(e) => setSourceUrl(e.target.value)} required spellCheck={false} />
            <button
              type="button"
              className="btn-secondary whitespace-nowrap"
              onClick={() => suggestMut.mutate(sourceUrl.trim())}
              disabled={suggestMut.isPending || !sourceUrl.trim()}
            >
              {suggestMut.isPending ? '分析中...' : '🔍 推荐白名单'}
            </button>
          </div>
          <p className="mt-1 text-xs text-muted">支持公众号文章、H5 互动页等。客户端会用 WebView 打开并拦截白名单外的跳转。填写 URL 后可点击「推荐白名单」自动分析。</p>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">白名单域名（逗号分隔，留空用默认）</label>
          <input className="input" placeholder="mp.weixin.qq.com, mmbiz.qpic.cn" value={whitelistDomains} onChange={(e) => setWhitelistDomains(e.target.value)} spellCheck={false} />
          <p className="mt-1 text-xs text-muted">只允许跳转到这些域名，防止点击广告/外链。空则使用默认白名单。</p>
        </div>
        <SubjectSelect value={subject} onChange={setSubject} />
        <GradeField value={grade} onChange={setGrade} />
        <SeriesSelect value={seriesId} onChange={setSeriesId} series={series} />
        <ImageUpload value={coverUrl} onChange={setCoverUrl} />
        <TagField value={tagIds} onChange={setTagIds} />
        <div>
          <label className="mb-1 block text-xs text-muted">排序权重</label>
          <input className="input" type="number" value={sortOrder} onChange={(e) => setSortOrder(Number(e.target.value))} />
        </div>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '添加文章'}
        </button>
      </form>
    </Modal>
  );
}

// --- Folder Import Modal ---
// Picks an Alist folder, previews the PDF tree, and creates a ReadingSeries +
// ReadingBook rows. Mirrors the video Import.tsx wizard but simplified.

interface ReadingImportNode {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  hash: string;
  type: string; // series | book | pass-through | exclude
  children?: ReadingImportNode[];
}

function ReadingImportModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
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
      for (const c of node.children) setSubtreeType(c, type === 'exclude' ? 'exclude' : c.is_dir ? (c === node.children[0] && node.type === 'series' ? 'series' : 'pass-through') : 'book');
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
            <input className="input" placeholder="/books/恐龙系列" value={path} onChange={(e) => setPath(e.target.value)} spellCheck={false} />
            <button className="btn-secondary whitespace-nowrap" onClick={() => setBrowsing(true)}>浏览...</button>
            <button className="btn-primary whitespace-nowrap" onClick={() => previewMut.mutate(path)} disabled={previewMut.isPending}>
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
                    <option key={s.key} value={s.key}>{s.emoji} {s.label}</option>
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

            <button className="btn-primary w-full" onClick={() => executeMut.mutate()} disabled={executeMut.isPending}>
              {executeMut.isPending ? '导入中...' : `✓ 确认导入 ${countBooks(tree)} 本书`}
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

function ReadingImportTree({ node, onToggle, depth = 0 }: { node: ReadingImportNode; onToggle: (n: ReadingImportNode) => void; depth?: number }) {
  const excluded = node.type === 'exclude';
  return (
    <div style={{ marginLeft: depth > 0 ? 16 : 0 }}>
      <button
        type="button"
        onClick={() => onToggle(node)}
        className={`flex w-full items-center gap-2 rounded px-2 py-1 text-left text-sm transition hover:bg-card ${excluded ? 'opacity-40' : ''}`}
      >
        <span>{node.is_dir ? (depth === 0 ? '📁' : '📂') : '📕'}</span>
        <span className="flex-1 truncate" style={{ textDecoration: excluded ? 'line-through' : 'none' }}>{node.name}</span>
        {!node.is_dir && node.size > 0 && (
          <span className="text-xs text-muted">{(node.size / 1024 / 1024).toFixed(1)} MB</span>
        )}
        <span className={`text-xs ${excluded ? 'text-bad' : 'text-good'}`}>{excluded ? '✗' : '✓'}</span>
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
