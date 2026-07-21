// BookModal — create / edit a PDF ReadingBook. The PDF lives on the network
// disk (file_relative_path picked via PathBrowser); the client downloads +
// caches it on first open. On save it fires createReadingBook or
// updateReadingBook, invalidates the books + series caches, and closes.

import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { ReadingBook, ReadingSeries } from '../../lib/types';
import { Modal } from '../../components/ui';
import { ImageUpload } from '../../components/inputs';
import { PathBrowser } from '../../components/PathBrowser';
import { useToast } from '../../lib/toast';
import { SubjectSelect, GradeField, TagField, SeriesSelect } from './shared';

export function BookModal({
  book,
  series,
  onClose,
}: {
  book: ReadingBook | null;
  series: ReadingSeries[];
  onClose: () => void;
}) {
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
      setTitle('');
      setSeriesId(0);
      setGrade('universal');
      setSubject('');
      setFileRelativePath('');
      setCoverUrl('');
      setSortOrder(0);
      setTagIds([]);
    }
  }, [book]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        series_id: seriesId,
        sort_order: sortOrder,
        title: title.trim(),
        file_relative_path: fileRelativePath,
        cover_url: coverUrl,
        grade,
        subject,
        tag_ids: tagIds,
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
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">标题</label>
          <input
            className="input"
            placeholder="如：恐龙百科"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            required
            autoFocus
          />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">PDF 文件（从网盘选择）</label>
          <div className="flex gap-2">
            <input
              className="input"
              placeholder="网盘相对路径"
              value={fileRelativePath}
              onChange={(e) => setFileRelativePath(e.target.value)}
              required
              spellCheck={false}
            />
            <button type="button" className="btn-secondary whitespace-nowrap" onClick={() => setBrowsing(true)}>
              浏览...
            </button>
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
          <input
            className="input"
            type="number"
            value={sortOrder}
            onChange={(e) => setSortOrder(Number(e.target.value))}
          />
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
