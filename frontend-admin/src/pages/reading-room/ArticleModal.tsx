// ArticleModal — create / edit a web ReadingArticle. The article is rendered
// in a client WebView with a whitelist of allowed navigation domains; the
// 推荐白名单 button calls suggestWhitelist to auto-extract domains from the
// live URL and merge them into the field. On save it fires
// createReadingArticle or updateReadingArticle and closes.

import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Search } from 'lucide-react';
import { api } from '../../lib/api';
import type { ReadingArticle, ReadingSeries } from '../../lib/types';
import { Modal } from '../../components/ui';
import { ImageUpload } from '../../components/inputs';
import { useToast } from '../../lib/toast';
import { SubjectSelect, GradeField, TagField, SeriesSelect, parseWhitelistForDisplay } from './shared';

export function ArticleModal({
  article,
  series,
  onClose,
}: {
  article: ReadingArticle | null;
  series: ReadingSeries[];
  onClose: () => void;
}) {
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
      setTitle('');
      setSeriesId(0);
      setGrade('universal');
      setSubject('');
      setSourceUrl('');
      setWhitelistDomains('');
      setCoverUrl('');
      setSortOrder(0);
      setTagIds([]);
    }
  }, [article]);

  const saveMut = useMutation({
    mutationFn: async () => {
      const body = {
        series_id: seriesId,
        sort_order: sortOrder,
        title: title.trim(),
        source_url: sourceUrl.trim(),
        whitelist_domains: whitelistDomains.trim(),
        cover_url: coverUrl,
        grade,
        subject,
        tag_ids: tagIds,
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
            placeholder="如：上博展厅导览"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            required
            autoFocus
          />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">网页 URL</label>
          <div className="flex gap-2">
            <input
              className="input"
              placeholder="https://mp.weixin.qq.com/..."
              value={sourceUrl}
              onChange={(e) => setSourceUrl(e.target.value)}
              required
              spellCheck={false}
            />
            <button
              type="button"
              className="btn-secondary inline-flex items-center gap-1.5 whitespace-nowrap"
              onClick={() => suggestMut.mutate(sourceUrl.trim())}
              disabled={suggestMut.isPending || !sourceUrl.trim()}
            >
              {suggestMut.isPending ? (
                '分析中...'
              ) : (
                <>
                  <Search size={14} /> 推荐白名单
                </>
              )}
            </button>
          </div>
          <p className="mt-1 text-xs text-muted">
            支持公众号文章、H5 互动页等。客户端会用 WebView 打开并拦截白名单外的跳转。填写 URL
            后可点击「推荐白名单」自动分析。
          </p>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">白名单域名（逗号分隔，留空用默认）</label>
          <input
            className="input"
            placeholder="mp.weixin.qq.com, mmbiz.qpic.cn"
            value={whitelistDomains}
            onChange={(e) => setWhitelistDomains(e.target.value)}
            spellCheck={false}
          />
          <p className="mt-1 text-xs text-muted">只允许跳转到这些域名，防止点击广告/外链。空则使用默认白名单。</p>
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
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '添加文章'}
        </button>
      </form>
    </Modal>
  );
}
