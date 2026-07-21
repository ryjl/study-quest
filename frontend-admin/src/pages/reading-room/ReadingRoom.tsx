// Reading Room admin page — manages series (container), PDF books, and web
// articles. Mirrors the Courses page structure (filter bar + collapsible series
// cards + standalone section) but simpler (no chapter tree, no probe worker).
//
// Top-level shell only: filter bar + series/books/articles lists. The create /
// edit modals + the import wizard live in sibling files in this folder.

import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { FolderInput, Plus, Library, FileText, Globe, BookOpen, BookMarked } from 'lucide-react';
import { api } from '../../lib/api';
import type { ReadingArticle, ReadingBook, ReadingSeries } from '../../lib/types';
import { subjectMeta, gradeDisplay } from '../../lib/types';
import { useSubjects } from '../../lib/useSubjects';
import { useConfirm } from '../../lib/toast';
import { LoadingState, EmptyState, SubjectIcon } from '../../components/ui';
import { PageHeader } from '../../components/PageHeader';
import { useTypedMutation } from '../../lib/useTypedMutation';
import { SeriesChildren, ReadingItemRow } from './shared';
import { SeriesModal } from './SeriesModal';
import { BookModal } from './BookModal';
import { ArticleModal } from './ArticleModal';
import { ReadingImportModal } from './ImportModal';

export function ReadingRoom() {
  const qc = useQueryClient();
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
  const delSeriesMut = useTypedMutation({
    mutationFn: api.deleteReadingSeries,
    successMsg: '系列已删除',
    invalidateKeys: [['reading-series'], ['reading-books'], ['reading-articles']],
    errorMsg: '删除失败',
  });
  const delBookMut = useTypedMutation({
    mutationFn: api.deleteReadingBook,
    successMsg: '书籍已删除',
    invalidateKeys: [['reading-series'], ['reading-books'], ['reading-articles']],
    errorMsg: '删除失败',
  });
  const delArticleMut = useTypedMutation({
    mutationFn: api.deleteReadingArticle,
    successMsg: '文章已删除',
    invalidateKeys: [['reading-series'], ['reading-books'], ['reading-articles']],
    errorMsg: '删除失败',
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
            <button className="btn-secondary inline-flex items-center gap-1.5" onClick={() => setImporting(true)}>
              <FolderInput size={14} /> 从文件夹导入
            </button>
            <button className="btn-secondary inline-flex items-center gap-1.5" onClick={() => setCreatingBook(true)}>
              <Plus size={14} /> 添加书籍
            </button>
            <button className="btn-secondary inline-flex items-center gap-1.5" onClick={() => setCreatingArticle(true)}>
              <Plus size={14} /> 添加文章
            </button>
            <button className="btn-primary inline-flex items-center gap-1.5" onClick={() => setCreatingSeries(true)}>
              <Plus size={14} /> 新建系列
            </button>
          </div>
        }
      />

      {/* Filter bar */}
      <div className="mb-5 flex flex-wrap items-center gap-3 rounded-lg border border-border bg-card p-3">
        <input
          className="input max-w-xs"
          placeholder="搜索标题..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <select
          className="input max-w-[180px]"
          value={subjectFilter}
          onChange={(e) => setSubjectFilter(e.target.value)}
        >
          <option value="">全部科目</option>
          {(subjectsQ.data ?? []).map((s) => (
            <option key={s.key} value={s.key}>
              {s.label}
            </option>
          ))}
        </select>
        <span className="text-sm text-muted">
          {filteredSeries.length} 系列 · {filteredBooks.length} 散本 · {filteredArticles.length} 散文
        </span>
      </div>

      {series.length === 0 && standaloneBooks.length === 0 && standaloneArticles.length === 0 && (
        <EmptyState
          icon={<BookOpen size={28} />}
          title="阅读室还是空的"
          hint="添加 PDF 绘本/试卷或网页文章，或新建一个系列来组织同主题内容。"
        />
      )}

      {/* Series section */}
      {filteredSeries.length > 0 && (
        <div className="mb-6">
          <h2 className="mb-3 inline-flex items-center gap-1.5 text-sm font-semibold text-muted">
            <Library size={14} /> 系列
          </h2>
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
                      <div
                        className="flex h-14 w-14 items-center justify-center rounded-lg bg-card-2"
                        style={{ color: subj.color }}
                      >
                        <SubjectIcon subject={s.subject} size={22} />
                      </div>
                    )}
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-txt">{s.title}</span>
                        <span
                          className="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium"
                          style={{ backgroundColor: `${subj.color}20`, color: subj.color }}
                        >
                          <SubjectIcon subject={s.subject} size={12} />
                          {subj.label}
                        </span>
                        <span className="text-xs text-muted">{gradeDisplay(s.grade)}</span>
                      </div>
                      <p className="mt-0.5 truncate text-xs text-muted">
                        {s.book_count} 本书 · {s.article_count} 篇文章
                        {s.description ? ` · ${s.description}` : ''}
                      </p>
                    </div>
                    <div className="flex gap-1.5">
                      <button
                        className="btn-ghost btn-sm"
                        onClick={() => {
                          if (isOpen) {
                            setExpandedSeries((prev) => {
                              const n = new Set(prev);
                              n.delete(s.id);
                              return n;
                            });
                          } else {
                            setExpandedSeries((prev) => new Set(prev).add(s.id));
                          }
                        }}
                      >
                        {isOpen ? '收起' : '展开'}
                      </button>
                      <button className="btn-ghost btn-sm" onClick={() => setEditingSeries(s)}>
                        编辑
                      </button>
                      <button
                        className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                        onClick={async () => {
                          if (
                            await confirm({
                              message: `删除系列「${s.title}」？`,
                              detail: '系列下的书籍/文章会变为散本，不会被删除。',
                              danger: true,
                            })
                          )
                            delSeriesMut.mutate(s.id);
                        }}
                        disabled={delSeriesMut.isPending}
                      >
                        删除
                      </button>
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
          <h2 className="mb-3 inline-flex items-center gap-1.5 text-sm font-semibold text-muted">
            <FileText size={14} /> 散本（PDF）
          </h2>
          <div className="space-y-2">
            {filteredBooks.map((b) => {
              const subj = subjectMeta(b.subject);
              return (
                <ReadingItemRow
                  key={b.id}
                  icon={<BookMarked size={16} />}
                  title={b.title}
                  subtitle={`${gradeDisplay(b.grade)} · ${b.page_count ?? '?'}页 · ${b.file_relative_path}`}
                  subjectColor={subj.color}
                  subjectLabel={subj.label}
                  onEdit={() => setEditingBook(b)}
                  onDelete={async () => {
                    if (
                      await confirm({
                        message: `删除书籍「${b.title}」？`,
                        detail: '将同时删除该用户的阅读进度记录。',
                        danger: true,
                      })
                    )
                      delBookMut.mutate(b.id);
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
          <h2 className="mb-3 inline-flex items-center gap-1.5 text-sm font-semibold text-muted">
            <Globe size={14} /> 散文（网页）
          </h2>
          <div className="space-y-2">
            {filteredArticles.map((a) => {
              const subj = subjectMeta(a.subject);
              return (
                <ReadingItemRow
                  key={a.id}
                  icon={<Globe size={16} />}
                  title={a.title}
                  subtitle={`${gradeDisplay(a.grade)} · ${a.source_url}`}
                  subjectColor={subj.color}
                  subjectLabel={subj.label}
                  onEdit={() => setEditingArticle(a)}
                  onDelete={async () => {
                    if (await confirm({ message: `删除文章「${a.title}」？`, danger: true }))
                      delArticleMut.mutate(a.id);
                  }}
                />
              );
            })}
          </div>
        </div>
      )}

      {/* Modals */}
      {(creatingSeries || editingSeries) && (
        <SeriesModal
          series={editingSeries}
          onClose={() => {
            setCreatingSeries(false);
            setEditingSeries(null);
          }}
        />
      )}
      {(creatingBook || editingBook) && (
        <BookModal
          book={editingBook}
          series={series}
          onClose={() => {
            setCreatingBook(false);
            setEditingBook(null);
          }}
        />
      )}
      {(creatingArticle || editingArticle) && (
        <ArticleModal
          article={editingArticle}
          series={series}
          onClose={() => {
            setCreatingArticle(false);
            setEditingArticle(null);
          }}
        />
      )}
      {importing && <ReadingImportModal onClose={() => setImporting(false)} onDone={invalidateAll} />}
    </div>
  );
}
