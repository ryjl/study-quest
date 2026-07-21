// UserDetailDrawer — the authorization drawer. Redesigned as a self-contained,
// product-grade panel: summary card on top, a sticky "commit bar" that batches
// every access change into ONE save (no per-toggle network calls), grouped
// sections (course access grouped by subject, reading by type), and secondary
// info (unlock rhythm, points ledger, badges) collapsed by default.
//
// Staged save: the drawer keeps local draft Sets for course + reading access.
// Toggles mutate the draft only. A single 保存 button diffs the drafts against
// the live `user` prop and fires all per-item grant/revoke requests in parallel
// (Promise.allSettled), then reports success/partial-failure counts via toast.
// The live `user` prop is read fresh from the parent's users query, so after a
// save invalidates `['users']`, original realigns with the draft and the diff
// drops back to zero automatically.

import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { Course, SubjectMeta, User } from '../../lib/types';
import { subjectMeta } from '../../lib/types';
import { Drawer, Tag, Section, SubjectIcon } from '../../components/ui';
import { UserCourseUnlockRow } from '../../components/UserCourseUnlockRow';
import { relativeTime, formatWatchTime } from '../../lib/format';
import { useSubjects } from '../../lib/useSubjects';
import { useToast } from '../../lib/toast';
import { roleMeta } from './Users';
import { StorageWhitelistSection } from './StorageWhitelistSection';
import {
  AlertTriangle,
  Award,
  BookOpen,
  BookMarked,
  Database,
  Folder,
  Globe,
  Library,
  Lock,
} from 'lucide-react';

export function UserDetailDrawer({ user, onClose }: { user: User; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const subjectsQ = useSubjects();
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const ledgerQ = useQuery({ queryKey: ['ledger', user.id], queryFn: () => api.userLedger(user.id, 10) });
  const badgesQ = useQuery({ queryKey: ['user-badges', user.id], queryFn: () => api.userBadges(user.id) });
  // Reading Room catalogs — for the checkbox lists.
  const readingSeriesQ = useQuery({ queryKey: ['reading-series'], queryFn: api.listReadingSeries });
  const readingBooksQ = useQuery({ queryKey: ['reading-books'], queryFn: api.listReadingBooks });
  const readingArticlesQ = useQuery({ queryKey: ['reading-articles'], queryFn: api.listReadingArticles });

  const courses = coursesQ.data ?? [];
  const readingSeries = readingSeriesQ.data ?? [];
  const readingBooks = readingBooksQ.data ?? [];
  const readingArticles = readingArticlesQ.data ?? [];
  const ledger = ledgerQ.data ?? [];
  const badges = badgesQ.data ?? [];
  const unlocked = badges.filter((b) => b.unlocked);
  const subjects = subjectsQ.data ?? [];

  // Live "original" baselines, recomputed each render from the fresh user prop.
  // After a save invalidates ['users'], the parent passes a new user object and
  // these move to match the drafts → diff becomes zero.
  const origCourse = new Set(user.course_access ?? []);
  const origSeries = new Set(user.reading_series_access ?? []);
  const origBooks = new Set(user.reading_book_access ?? []);
  const origArticles = new Set(user.reading_article_access ?? []);

  // Drafts — the editable working copy. Resynced ONLY on user.id change (i.e.
  // the drawer opened for a different user). We deliberately do NOT resync on
  // every user prop update: a successful save updates the prop, and we don't
  // want to fight an in-flight draft. Because the diff is computed against the
  // live original, once the save lands the diff collapses to zero naturally.
  const [draftCourse, setDraftCourse] = useState<Set<number>>(() => new Set(user.course_access ?? []));
  const [draftSeries, setDraftSeries] = useState<Set<number>>(() => new Set(user.reading_series_access ?? []));
  const [draftBooks, setDraftBooks] = useState<Set<number>>(() => new Set(user.reading_book_access ?? []));
  const [draftArticles, setDraftArticles] = useState<Set<number>>(() => new Set(user.reading_article_access ?? []));
  const [saving, setSaving] = useState(false);
  const [courseSearch, setCourseSearch] = useState('');

  useEffect(() => {
    setDraftCourse(new Set(user.course_access ?? []));
    setDraftSeries(new Set(user.reading_series_access ?? []));
    setDraftBooks(new Set(user.reading_book_access ?? []));
    setDraftArticles(new Set(user.reading_article_access ?? []));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [user.id]);

  // Diffs across all four access scopes → one combined commit count.
  const diff = useMemo(() => {
    let added = 0,
      removed = 0;
    draftCourse.forEach((id) => {
      if (!origCourse.has(id)) added++;
    });
    origCourse.forEach((id) => {
      if (!draftCourse.has(id)) removed++;
    });
    draftSeries.forEach((id) => {
      if (!origSeries.has(id)) added++;
    });
    origSeries.forEach((id) => {
      if (!draftSeries.has(id)) removed++;
    });
    draftBooks.forEach((id) => {
      if (!origBooks.has(id)) added++;
    });
    origBooks.forEach((id) => {
      if (!draftBooks.has(id)) removed++;
    });
    draftArticles.forEach((id) => {
      if (!origArticles.has(id)) added++;
    });
    origArticles.forEach((id) => {
      if (!draftArticles.has(id)) removed++;
    });
    return added + removed;
  }, [draftCourse, draftSeries, draftBooks, draftArticles, origCourse, origSeries, origBooks, origArticles]);

  const dirty = diff > 0;

  // ---- draft mutators (NO network) ----
  const toggleIn = (set: Set<number>, id: number, on: boolean): Set<number> => {
    const next = new Set(set);
    if (on) next.add(id);
    else next.delete(id);
    return next;
  };
  const toggleCourse = (id: number, on: boolean) => setDraftCourse((p) => toggleIn(p, id, on));
  const toggleSeries = (id: number, on: boolean) => setDraftSeries((p) => toggleIn(p, id, on));
  const toggleBook = (id: number, on: boolean) => setDraftBooks((p) => toggleIn(p, id, on));
  const toggleArticle = (id: number, on: boolean) => setDraftArticles((p) => toggleIn(p, id, on));

  const selectAllCourses = () => setDraftCourse(new Set(courses.map((c) => c.id)));
  const clearAllCourses = () => setDraftCourse(new Set());
  const resetDrafts = () => {
    setDraftCourse(new Set(origCourse));
    setDraftSeries(new Set(origSeries));
    setDraftBooks(new Set(origBooks));
    setDraftArticles(new Set(origArticles));
  };

  // ---- the single commit: diff every scope, fire all per-item calls in
  //      parallel via Promise.allSettled, report counts. grant_all/revoke_all
  //      bulk endpoints are intentionally NOT used — staged per-item keeps the
  //      UX consistent and lets partial failures (e.g. storage-whitelist 403)
  //      report per-item instead of failing the whole batch. ----
  const save = async () => {
    const tasks: Promise<unknown>[] = [];
    draftCourse.forEach((id) => {
      if (!origCourse.has(id)) tasks.push(api.grantAccess(user.id, id));
    });
    origCourse.forEach((id) => {
      if (!draftCourse.has(id)) tasks.push(api.revokeAccess(user.id, id));
    });
    draftSeries.forEach((id) => {
      if (!origSeries.has(id)) tasks.push(api.grantReadingAccess(user.id, 'series', id));
    });
    origSeries.forEach((id) => {
      if (!draftSeries.has(id)) tasks.push(api.revokeReadingAccess(user.id, 'series', id));
    });
    draftBooks.forEach((id) => {
      if (!origBooks.has(id)) tasks.push(api.grantReadingAccess(user.id, 'book', id));
    });
    origBooks.forEach((id) => {
      if (!draftBooks.has(id)) tasks.push(api.revokeReadingAccess(user.id, 'book', id));
    });
    draftArticles.forEach((id) => {
      if (!origArticles.has(id)) tasks.push(api.grantReadingAccess(user.id, 'article', id));
    });
    origArticles.forEach((id) => {
      if (!draftArticles.has(id)) tasks.push(api.revokeReadingAccess(user.id, 'article', id));
    });

    setSaving(true);
    try {
      const results = await Promise.allSettled(tasks);
      const failed = results.filter((r) => r.status === 'rejected').length;
      const succeeded = results.length - failed;
      if (failed === 0) {
        toast.success(`已保存 ${succeeded} 项授权更改`);
      } else {
        toast.error(`${succeeded} 项已保存，${failed} 项失败（可能是存储源白名单限制）`);
      }
      qc.invalidateQueries({ queryKey: ['users'] });
    } finally {
      setSaving(false);
    }
  };

  // ---- course grouping by subject ----
  // Courses bucket into subject groups (sorted by subject sort_order when
  // available); courses with no resolvable subject land in "其他". The search
  // box filters visible rows without touching the draft.
  const subjectMetaFor = (key: string): SubjectMeta => {
    const found = subjects.find((s) => s.key === key) as SubjectMeta | undefined;
    return found ?? subjectMeta(key);
  };
  const groupedCourses = useMemo(() => {
    const q = courseSearch.trim().toLowerCase();
    const filtered = q ? courses.filter((c) => c.title.toLowerCase().includes(q)) : courses;
    const groups = new Map<string, Course[]>();
    for (const c of filtered) {
      const key = c.subject || '';
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key)!.push(c);
    }
    // Stable subject ordering by sort_order (fall back to label), then 其他 last.
    const sortedKeys = [...groups.keys()].sort((a, b) => {
      if (!a) return 1;
      if (!b) return -1;
      const ma = subjectMetaFor(a),
        mb = subjectMetaFor(b);
      const sa = ma.sort_order ?? 0,
        sb = mb.sort_order ?? 0;
      if (sa !== sb) return sa - sb;
      return ma.label.localeCompare(mb.label);
    });
    return sortedKeys.map((k) => ({ key: k, meta: subjectMetaFor(k), list: groups.get(k)! }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [courses, courseSearch, subjects]);

  const rm = roleMeta(user.role);
  const grantedCount = origCourse.size;
  // Granted course ids in catalog order (so the unlock rows match the checkbox
  // list, not the raw access array order).
  const grantedCourseIds = courses.filter((c) => origCourse.has(c.id)).map((c) => c.id);

  return (
    <Drawer open onClose={onClose} title={`用户授权 · ${user.nickname}`} width="lg">
      {/* ---- User summary card (always at top) ---- */}
      <div className="mb-4 flex items-center gap-4 rounded-lg border border-border bg-card-2/60 p-3.5">
        {user.avatar_url ? (
          <img
            src={user.avatar_url}
            alt=""
            className="h-14 w-14 rounded-full object-cover"
            onError={(e) => ((e.target as HTMLImageElement).style.opacity = '0.3')}
          />
        ) : (
          <div className="flex h-14 w-14 items-center justify-center rounded-full bg-card text-xl font-semibold">
            {user.nickname.slice(0, 1)}
          </div>
        )}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-lg font-bold text-txt">{user.nickname}</span>
            <span
              className="rounded px-2 py-0.5 text-xs font-semibold"
              style={{ backgroundColor: `${rm.color}20`, color: rm.color }}
            >
              {rm.label}
            </span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
            <span>
              <span className="text-txt">{grantedCount}</span> 课程
            </span>
            <span>
              <span className="text-txt">{origSeries.size + origBooks.size + origArticles.size}</span> 阅读项
            </span>
            <span>
              学习时长 <span className="text-txt">{formatWatchTime(user.watch_seconds)}</span>
            </span>
            <span>
              <Award size={12} className="inline text-warn" /> <span className="text-txt">{user.unlocked_badges ?? 0}</span> 徽章
            </span>
            <span>活跃 {user.last_active_at ? relativeTime(user.last_active_at) : '—'}</span>
          </div>
        </div>
      </div>

      {/* ---- Sticky commit bar (only when dirty) ---- */}
      {dirty && (
        <div className="sticky top-0 z-10 mb-4 flex items-center gap-3 rounded-lg border border-warn/40 bg-warn/10 px-3.5 py-2.5">
          <AlertTriangle size={15} className="flex-shrink-0 text-warn" />
          <span className="flex-1 text-sm font-medium text-txt">有 {diff} 项授权更改未保存</span>
          <button className="btn-ghost btn-sm" onClick={resetDrafts} disabled={saving}>
            放弃
          </button>
          <button className="btn-primary btn-sm" onClick={save} disabled={saving}>
            {saving ? '保存中…' : '保存'}
          </button>
        </div>
      )}

      <div className="space-y-4">
        {/* ---- 课程授权 ---- */}
        <Section
          title="课程授权"
          icon={<Library size={14} />}
          defaultOpen
          badge={`${draftCourse.size}/${courses.length}`}
          right={
            <>
              <button className="btn-ghost btn-sm" onClick={selectAllCourses} disabled={saving}>
                全部授权
              </button>
              <button className="btn-ghost btn-sm" onClick={clearAllCourses} disabled={saving}>
                全部撤销
              </button>
            </>
          }
        >
          <input
            className="input mb-3 text-sm"
            placeholder="搜索课程名称…"
            value={courseSearch}
            onChange={(e) => setCourseSearch(e.target.value)}
          />
          {groupedCourses.length === 0 ? (
            <p className="py-4 text-center text-sm text-muted">
              {courses.length === 0 ? '暂无课程' : '没有匹配的课程'}
            </p>
          ) : (
            <div className="space-y-3">
              {groupedCourses.map((g) => (
                <CourseSubjectGroup
                  key={g.key || '__other__'}
                  meta={g.meta}
                  courses={g.list}
                  draft={draftCourse}
                  onToggle={toggleCourse}
                  disabled={saving}
                />
              ))}
            </div>
          )}
        </Section>

        {/* ---- 阅读室授权 ---- */}
        <Section
          title="阅读室授权"
          icon={<BookOpen size={14} />}
          defaultOpen
          badge={`${draftSeries.size + draftBooks.size + draftArticles.size}/${readingSeries.length + readingBooks.length + readingArticles.length}`}
        >
          {readingSeries.length === 0 && readingBooks.length === 0 && readingArticles.length === 0 ? (
            <p className="text-sm text-muted">阅读室还没有内容，请先到阅读室页面添加。</p>
          ) : (
            <div className="space-y-3">
              {readingSeries.length > 0 && (
                <ReadingSubGroup
                  label="系列"
                  icon={<Folder size={14} />}
                  items={readingSeries.map((s) => ({ id: s.id, title: s.title, suffix: `${s.book_count + s.article_count} 项` }))}
                  draft={draftSeries}
                  onToggle={toggleSeries}
                  disabled={saving}
                />
              )}
              {readingBooks.length > 0 && (
                <ReadingSubGroup
                  label="书籍 PDF"
                  icon={<BookMarked size={14} />}
                  items={readingBooks.map((b) => ({ id: b.id, title: b.title }))}
                  draft={draftBooks}
                  onToggle={toggleBook}
                  disabled={saving}
                />
              )}
              {readingArticles.length > 0 && (
                <ReadingSubGroup
                  label="文章"
                  icon={<Globe size={14} />}
                  items={readingArticles.map((a) => ({ id: a.id, title: a.title }))}
                  draft={draftArticles}
                  onToggle={toggleArticle}
                  disabled={saving}
                />
              )}
            </div>
          )}
        </Section>

        {/* ---- 存储源白名单 (keeps its own staged save internally) ---- */}
        <Section
          title="存储源白名单"
          icon={<Database size={14} />}
          defaultOpen
          description="空列表 = 一个都不允许（播放前必须勾选至少一个源）"
        >
          <StorageWhitelistSection userId={user.id} current={user.storage_source_access ?? []} />
        </Section>

        {/* ---- 解锁节奏 (collapsed by default — the big space-eater) ---- */}
        <Section
          title="解锁节奏"
          icon={<Lock size={14} />}
          defaultOpen={false}
          description={grantedCount > 0 ? `已为 ${grantedCount} 门课程设置解锁节奏` : '暂无已授权课程'}
        >
          {grantedCourseIds.length === 0 ? (
            <p className="text-sm text-muted">授权课程后可在此调整每门课程的解锁节奏。</p>
          ) : (
            <div className="space-y-1.5">
              {grantedCourseIds.map((cid) => {
                const c = courses.find((x) => x.id === cid);
                return (
                  <UserCourseUnlockRow
                    key={cid}
                    userId={user.id}
                    courseId={cid}
                    courseTitle={c?.title ?? `#${cid}`}
                  />
                );
              })}
            </div>
          )}
        </Section>

        {/* ---- 积分与徽章 (secondary, collapsed by default) ---- */}
        <Section
          title="积分与徽章"
          icon={<Award size={14} />}
          defaultOpen={false}
          description={`积分 ${user.current_points ?? 0} · 已解锁 ${unlocked.length} 个徽章 · 流水近 ${ledger.length} 条`}
        >
          <div className="mb-4">
            <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">积分流水（近 10 条）</h4>
            {ledger.length === 0 ? (
              <p className="text-sm text-muted">暂无记录</p>
            ) : (
              <div className="space-y-1">
                {ledger.map((l) => (
                  <div
                    key={l.id}
                    className="flex items-center justify-between rounded-md bg-card-2 px-3 py-1.5 text-sm"
                  >
                    <span className="text-txt">{l.description || l.reason_type}</span>
                    <span className={`tabular-nums ${l.change_amount >= 0 ? 'text-good' : 'text-bad'}`}>
                      {l.change_amount >= 0 ? '+' : ''}
                      {l.change_amount}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
          <div>
            <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted">已解锁徽章 ({unlocked.length})</h4>
            {unlocked.length === 0 ? (
              <p className="text-sm text-muted">暂无</p>
            ) : (
              <div className="flex flex-wrap gap-1.5">
                {unlocked.map((b) => (
                  <Tag key={b.id} color="#fbbf24">
                    {b.title}
                  </Tag>
                ))}
              </div>
            )}
          </div>
        </Section>
      </div>
    </Drawer>
  );
}

// CourseSubjectGroup — one subject bucket inside 课程授权. Header shows the
// subject's emoji + label + (selected/total) count; the 全选组/清空组 buttons
// flip every course in this subject in the draft (no network).
function CourseSubjectGroup({
  meta,
  courses,
  draft,
  onToggle,
  disabled,
}: {
  meta: SubjectMeta;
  courses: Course[];
  draft: Set<number>;
  onToggle: (id: number, on: boolean) => void;
  disabled?: boolean;
}) {
  const selected = courses.filter((c) => draft.has(c.id)).length;
  const allSelected = selected === courses.length;
  const setGroup = (on: boolean) => {
    for (const c of courses) onToggle(c.id, on);
  };
  return (
    <div className="rounded-md border border-border bg-card-2/40">
      <div className="flex items-center justify-between gap-2 px-3 py-2">
        <div className="flex items-center gap-2 text-sm">
          <SubjectIcon subject={meta.key} size={14} />
          <span className="font-medium text-txt">{meta.label}</span>
          <span className="text-xs tabular-nums text-muted">
            {selected}/{courses.length}
          </span>
        </div>
        <div className="flex gap-1">
          <button className="btn-ghost btn-sm" onClick={() => setGroup(true)} disabled={disabled || allSelected}>
            全选组
          </button>
          <button
            className="btn-ghost btn-sm"
            onClick={() => setGroup(false)}
            disabled={disabled || selected === 0}
          >
            清空组
          </button>
        </div>
      </div>
      <div className="space-y-1 px-2 pb-2">
        {courses.map((c) => (
          <label
            key={c.id}
            className="flex items-center gap-2 rounded-md border border-border/60 bg-card px-3 py-1.5 text-sm hover:bg-card-2"
          >
            <input
              type="checkbox"
              checked={draft.has(c.id)}
              onChange={(e) => onToggle(c.id, e.target.checked)}
              disabled={disabled}
              className="h-4 w-4 accent-primary"
            />
            <span className="flex-1 truncate text-txt">{c.title}</span>
          </label>
        ))}
      </div>
    </div>
  );
}

// ReadingSubGroup — one type bucket (系列 / 书籍 / 文章) inside 阅读室授权.
// Same staged pattern: 全选/清空 flip the whole subgroup in the draft.
function ReadingSubGroup({
  label,
  icon,
  items,
  draft,
  onToggle,
  disabled,
}: {
  label: string;
  icon: React.ReactNode;
  items: { id: number; title: string; suffix?: string }[];
  draft: Set<number>;
  onToggle: (id: number, on: boolean) => void;
  disabled?: boolean;
}) {
  const selected = items.filter((i) => draft.has(i.id)).length;
  const setAll = (on: boolean) => {
    for (const i of items) onToggle(i.id, on);
  };
  return (
    <div className="rounded-md border border-border bg-card-2/40">
      <div className="flex items-center justify-between gap-2 px-3 py-2">
        <div className="flex items-center gap-2 text-sm">
          <span className="text-muted">{icon}</span>
          <span className="font-medium text-txt">{label}</span>
          <span className="text-xs tabular-nums text-muted">
            {selected}/{items.length}
          </span>
        </div>
        <div className="flex gap-1">
          <button className="btn-ghost btn-sm" onClick={() => setAll(true)} disabled={disabled}>
            全选
          </button>
          <button className="btn-ghost btn-sm" onClick={() => setAll(false)} disabled={disabled}>
            清空
          </button>
        </div>
      </div>
      <div className="max-h-48 space-y-1 overflow-auto px-2 pb-2">
        {items.map((i) => (
          <label
            key={i.id}
            className="flex items-center gap-2 rounded-md border border-border/60 bg-card px-3 py-1.5 text-sm hover:bg-card-2"
          >
            <input
              type="checkbox"
              checked={draft.has(i.id)}
              onChange={(e) => onToggle(i.id, e.target.checked)}
              disabled={disabled}
              className="h-4 w-4 accent-primary"
            />
            <span className="flex-1 truncate text-txt">{i.title}</span>
            {i.suffix && <span className="text-xs text-muted">{i.suffix}</span>}
          </label>
        ))}
      </div>
    </div>
  );
}
