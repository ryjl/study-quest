import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { Course, SubjectMeta, User, UserSession } from '../lib/types';
import { subjectMeta } from '../lib/types';
import { Modal, LoadingState, EmptyState, Drawer, Tag, Section, DropdownMenu, SubjectIcon } from '../components/ui';
import { PageHeader } from '../components/PageHeader';
import { ImageUpload } from '../components/inputs';
import { UserCourseUnlockRow } from '../components/UserCourseUnlockRow';
import { relativeTime } from '../lib/format';
import { useSubjects } from '../lib/useSubjects';
import {
  Plus,
  KeyRound,
  Pencil,
  Trash2,
  Smartphone,
  Star,
  AlertTriangle,
  Users as UsersIcon,
  BookOpen,
  Library,
  Folder,
  BookMarked,
  Globe,
  Database,
  Lock,
  Award,
} from 'lucide-react';

// Compact watch-time formatter. Uses raw seconds for sub-minute precision so
// a user who watched e.g. 40 seconds doesn't show a misleading "0 分".
function formatWatchTime(seconds?: number): string {
  if (seconds !== undefined && seconds > 0) {
    const s = Math.floor(seconds);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const rem = s % 60;
    if (h > 0) return rem === 0 ? (m === 0 ? `${h} 时` : `${h} 时 ${m} 分`) : `${h} 时 ${m} 分`;
    if (m > 0) return rem === 0 ? `${m} 分` : `${m} 分 ${rem} 秒`;
    return `${rem} 秒`;
  }
  return '0 分';
}
import { useToast, useConfirm } from '../lib/toast';
import { useStorageSources } from '../lib/useStorageSources';

const ROLES = [
  { key: 'student', label: '学生', color: '#60a5fa' },
  { key: 'teen', label: '青少年', color: '#fbbf24' },
  { key: 'parent', label: '家长', color: '#34d399' },
  { key: 'admin', label: '管理员', color: '#6366f1' },
];

function roleMeta(role: string) {
  return ROLES.find((r) => r.key === role) ?? ROLES[0];
}

export function Users() {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<User | null>(null);
  const [creating, setCreating] = useState(false);
  // Store only the user id so the drawer always reads the freshest user
  // object from the live `users` query (otherwise toggling access shows stale
  // checkboxes until the drawer is reopened).
  const [detailForId, setDetailForId] = useState<number | null>(null);
  // Which user's device sessions to show in the sessions modal (null = closed).
  const [sessionsForId, setSessionsForId] = useState<number | null>(null);

  const usersQ = useQuery({ queryKey: ['users'], queryFn: api.listUsers });
  const users = usersQ.data ?? [];
  // Fetch the course catalog once so each user row can show the NAMES of
  // granted courses (we only have course ids on the user object).
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const courseTitleById = new Map<number, string>(
    (coursesQ.data ?? []).map((c) => [c.id, c.title]),
  );

  const delMut = useMutation({
    mutationFn: api.deleteUser,
    onSuccess: () => {
      toast.success('用户已删除');
      qc.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <div>
      <PageHeader
        title="用户与授权"
        actions={
          <button className="btn-primary" onClick={() => setCreating(true)}>
            <Plus size={14} /> 新增用户
          </button>
        }
      />

      {usersQ.isLoading ? (
        <LoadingState />
      ) : users.length === 0 ? (
        <EmptyState icon={<UsersIcon size={32} />} title="暂无用户" hint="创建第一个学生账号开始使用" />
      ) : (
        <div className="overflow-hidden rounded-lg border border-border/60 bg-card">
          <table className="w-full">
            <thead>
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-muted">
                <th className="px-4 py-2.5 font-medium">用户</th>
                <th className="px-4 py-2.5 font-medium">角色</th>
                <th className="px-4 py-2.5 font-medium">授权 / 完课</th>
                <th className="px-4 py-2.5 font-medium">学习时长</th>
                <th className="px-4 py-2.5 font-medium">积分</th>
                <th className="px-4 py-2.5 font-medium">徽章</th>
                <th className="px-4 py-2.5 font-medium">最后活跃</th>
                <th className="px-4 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const rm = roleMeta(u.role);
                const access = u.course_access ?? [];
                return (
                  <tr key={u.id} className="border-b border-border/50 text-sm last:border-0 hover:bg-card-2/50">
                    <td className="px-4 py-2.5">
                      <div className="flex items-center gap-2.5">
                        {u.avatar_url ? (
                          <img src={u.avatar_url} alt="" className="h-8 w-8 rounded-full object-cover" onError={(e) => ((e.target as HTMLImageElement).style.opacity = '0.3')} />
                        ) : (
                          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-card-2 text-xs font-medium">{u.nickname.slice(0, 1)}</div>
                        )}
                        <span className="font-medium text-txt">{u.nickname}</span>
                      </div>
                    </td>
                    <td className="px-4 py-2.5">
                      <span className="rounded px-2 py-0.5 text-xs font-medium" style={{ backgroundColor: `${rm.color}1a`, color: rm.color }}>
                        {rm.label}
                      </span>
                    </td>
                    <td className="px-4 py-2.5">
                      <div className="tabular-nums text-txt">
                        {access.length} <span className="text-muted">门</span>
                      </div>
                      <div className="text-xs tabular-nums text-muted" title="已完成课时 / 可访问总课时">
                        {u.completed_episodes ?? 0} / {u.accessible_episodes ?? 0} 课时
                      </div>
                      {access.length > 0 && (
                        <div className="mt-1 flex flex-wrap gap-1">
                          {access.slice(0, 3).map((cid) => (
                            <span
                              key={cid}
                              className="max-w-[120px] truncate rounded bg-card-2 px-1.5 py-0.5 text-[10px] text-muted"
                              title={courseTitleById.get(cid) ?? `#${cid}`}
                            >
                              {courseTitleById.get(cid) ?? `#${cid}`}
                            </span>
                          ))}
                          {access.length > 3 && (
                            <span className="text-[10px] tabular-nums text-muted">等 {access.length} 门</span>
                          )}
                        </div>
                      )}
                    </td>
                    <td className="px-4 py-2.5 tabular-nums text-muted">{formatWatchTime(u.watch_seconds)}</td>
                    <td className="px-4 py-2.5">
                      <span className="inline-flex items-center gap-1 tabular-nums text-warn">
                        <Star size={12} /> {u.current_points ?? 0}
                      </span>
                    </td>
                    <td className="px-4 py-2.5">
                      <span className="tabular-nums text-txt">{u.unlocked_badges ?? 0}</span>
                      <span className="tabular-nums text-muted"> / {u.total_badges ?? 0}</span>
                    </td>
                    <td className="px-4 py-2.5 tabular-nums text-muted" title={u.last_active_at ? new Date(u.last_active_at).toLocaleString() : '从未学习'}>
                      {u.last_active_at ? relativeTime(u.last_active_at) : '—'}
                    </td>
                    <td className="px-4 py-2.5">
                      {/* Primary action (授权) stays inline; the rest collapse into ⋯. */}
                      <div className="flex items-center justify-end gap-1">
                        <button className="btn-ghost btn-sm" onClick={() => setDetailForId(u.id)} title="管理授权">
                          <KeyRound size={14} />
                        </button>
                        <DropdownMenu
                          align="right"
                          items={[
                            { label: '管理授权', icon: <KeyRound size={14} />, onClick: () => setDetailForId(u.id) },
                            { label: '设备管理', icon: <Smartphone size={14} />, onClick: () => setSessionsForId(u.id) },
                            { label: '编辑用户', icon: <Pencil size={14} />, onClick: () => setEditing(u) },
                            {
                              label: '删除用户',
                              icon: <Trash2 size={14} />,
                              danger: true,
                              onClick: async () => {
                                if (await confirm({ message: `删除用户「${u.nickname}」？`, detail: '将一并删除其学习进度、积分与徽章记录。', danger: true })) delMut.mutate(u.id);
                              },
                            },
                          ]}
                        />
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {(editing || creating) && (
        <UserEditModal
          user={editing}
          onClose={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={() => {
            setEditing(null);
            setCreating(false);
            qc.invalidateQueries({ queryKey: ['users'] });
          }}
        />
      )}

      {detailForId != null && (() => {
        // Look up the live user so access toggles reflect immediately.
        const detailUser = users.find((u) => u.id === detailForId);
        if (!detailUser) return null; // user deleted → close drawer
        // The drawer is now self-contained: it manages its own staged draft
        // state and calls the access API directly on save (no per-toggle
        // callbacks from the parent).
        return <UserDetailDrawer user={detailUser} onClose={() => setDetailForId(null)} />;
      })()}

      {sessionsForId != null && (
        <UserSessionsModal
          userId={sessionsForId}
          onClose={() => setSessionsForId(null)}
        />
      )}
    </div>
  );
}

function UserEditModal({ user, onClose, onSaved }: { user: User | null; onClose: () => void; onSaved: () => void }) {
  const isEdit = !!user;
  const toast = useToast();
  const [nickname, setNickname] = useState(user?.nickname ?? '');
  const [avatarUrl, setAvatarUrl] = useState(user?.avatar_url ?? '');
  const [pin, setPin] = useState('');
  const [role, setRole] = useState(user?.role ?? 'student');

  const saveMut = useMutation({
    mutationFn: async () => {
      if (!nickname.trim()) throw new Error('请输入昵称');
      if (!isEdit && !/^\d{4,6}$/.test(pin)) throw new Error('PIN 必须为 4-6 位数字');
      if (isEdit) {
        const body: { nickname: string; avatar_url?: string; pin?: string; role: string } = { nickname: nickname.trim(), avatar_url: avatarUrl, role };
        if (pin) body.pin = pin;
        return api.updateUser(user!.id, body);
      }
      return api.createUser({ nickname: nickname.trim(), avatar_url: avatarUrl, pin, role });
    },
    onSuccess: () => {
      toast.success(isEdit ? '用户已更新' : '用户已创建');
      onSaved();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <Modal open onClose={onClose} title={isEdit ? '编辑用户' : '新增用户'} size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">昵称</label>
          <input className="input" value={nickname} onChange={(e) => setNickname(e.target.value)} required autoFocus />
        </div>
        <ImageUpload label="头像" value={avatarUrl} onChange={setAvatarUrl} />
        <div>
          <label className="mb-1 block text-xs text-muted">
            PIN 码 {isEdit && <span className="text-muted">（留空不修改）</span>}
          </label>
          <input type="password" className="input" value={pin} onChange={(e) => setPin(e.target.value)} placeholder="4-6 位数字" required={!isEdit} pattern="\d{4,6}" />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">角色</label>
          <select className="input" value={role} onChange={(e) => setRole(e.target.value)}>
            {ROLES.map((r) => (
              <option key={r.key} value={r.key}>
                {r.label} ({r.key})
              </option>
            ))}
          </select>
        </div>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : '保存'}
        </button>
      </form>
    </Modal>
  );
}

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
function UserDetailDrawer({ user, onClose }: { user: User; onClose: () => void }) {
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
    let added = 0, removed = 0;
    draftCourse.forEach((id) => { if (!origCourse.has(id)) added++; });
    origCourse.forEach((id) => { if (!draftCourse.has(id)) removed++; });
    draftSeries.forEach((id) => { if (!origSeries.has(id)) added++; });
    origSeries.forEach((id) => { if (!draftSeries.has(id)) removed++; });
    draftBooks.forEach((id) => { if (!origBooks.has(id)) added++; });
    origBooks.forEach((id) => { if (!draftBooks.has(id)) removed++; });
    draftArticles.forEach((id) => { if (!origArticles.has(id)) added++; });
    origArticles.forEach((id) => { if (!draftArticles.has(id)) removed++; });
    return added + removed;
  }, [draftCourse, draftSeries, draftBooks, draftArticles, origCourse, origSeries, origBooks, origArticles]);

  const dirty = diff > 0;

  // ---- draft mutators (NO network) ----
  const toggleIn = (set: Set<number>, id: number, on: boolean): Set<number> => {
    const next = new Set(set);
    if (on) next.add(id); else next.delete(id);
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
    draftCourse.forEach((id) => { if (!origCourse.has(id)) tasks.push(api.grantAccess(user.id, id)); });
    origCourse.forEach((id) => { if (!draftCourse.has(id)) tasks.push(api.revokeAccess(user.id, id)); });
    draftSeries.forEach((id) => { if (!origSeries.has(id)) tasks.push(api.grantReadingAccess(user.id, 'series', id)); });
    origSeries.forEach((id) => { if (!draftSeries.has(id)) tasks.push(api.revokeReadingAccess(user.id, 'series', id)); });
    draftBooks.forEach((id) => { if (!origBooks.has(id)) tasks.push(api.grantReadingAccess(user.id, 'book', id)); });
    origBooks.forEach((id) => { if (!draftBooks.has(id)) tasks.push(api.revokeReadingAccess(user.id, 'book', id)); });
    draftArticles.forEach((id) => { if (!origArticles.has(id)) tasks.push(api.grantReadingAccess(user.id, 'article', id)); });
    origArticles.forEach((id) => { if (!draftArticles.has(id)) tasks.push(api.revokeReadingAccess(user.id, 'article', id)); });

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
      const ma = subjectMetaFor(a), mb = subjectMetaFor(b);
      const sa = ma.sort_order ?? 0, sb = mb.sort_order ?? 0;
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
            <span><span className="text-txt">{grantedCount}</span> 课程</span>
            <span><span className="text-txt">{origSeries.size + origBooks.size + origArticles.size}</span> 阅读项</span>
            <span>学习时长 <span className="text-txt">{formatWatchTime(user.watch_seconds)}</span></span>
            <span><Award size={12} className="inline text-warn" /> <span className="text-txt">{user.unlocked_badges ?? 0}</span> 徽章</span>
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
              <button className="btn-ghost btn-sm" onClick={selectAllCourses} disabled={saving}>全部授权</button>
              <button className="btn-ghost btn-sm" onClick={clearAllCourses} disabled={saving}>全部撤销</button>
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
        <Section title="存储源白名单" icon={<Database size={14} />} defaultOpen description="空列表 = 一个都不允许（播放前必须勾选至少一个源）">
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
                  <div key={l.id} className="flex items-center justify-between rounded-md bg-card-2 px-3 py-1.5 text-sm">
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
          <span className="text-xs tabular-nums text-muted">{selected}/{courses.length}</span>
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
          <span className="text-xs tabular-nums text-muted">{selected}/{items.length}</span>
        </div>
        <div className="flex gap-1">
          <button className="btn-ghost btn-sm" onClick={() => setAll(true)} disabled={disabled}>全选</button>
          <button className="btn-ghost btn-sm" onClick={() => setAll(false)} disabled={disabled}>清空</button>
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

// UserSessionsModal lists a user's active device sessions and lets the admin
// revoke one ("踢下线") or all ("全部下线"), and relabel a device with a
// freeform note (e.g. "客厅那台 iPad"). Display rule: note → device_name → UA
// snippet, so even a client that didn't send device_name is identifiable.
function UserSessionsModal({ userId, onClose }: { userId: number; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const confirm = useConfirm();
  const sessionsQ = useQuery({
    queryKey: ['user-sessions', userId],
    queryFn: () => api.listUserSessions(userId),
  });

  const revokeOne = useMutation({
    mutationFn: (token: string) => api.revokeUserSession(userId, token),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['user-sessions', userId] });
      toast.success('已踢下线');
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const revokeAll = useMutation({
    mutationFn: () => api.revokeAllUserSessions(userId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['user-sessions', userId] });
      toast.success('已将该用户所有设备下线');
    },
    onError: (e: Error) => toast.error(e.message),
  });

  const saveNote = useMutation({
    mutationFn: ({ token, note }: { token: string; note: string }) => api.updateSessionNote(token, note),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['user-sessions', userId] }),
    onError: (e: Error) => toast.error(e.message),
  });

  const sessions = sessionsQ.data ?? [];

  return (
    <Modal open onClose={onClose} title="设备管理" size="lg">
      <div className="space-y-3">
        <p className="text-sm text-muted">
          列出该用户当前登录的设备。可单独或全部下线（下线后该设备下次请求会立即被踢回登录页），
          也可为每台设备加备注便于识别。
        </p>

        {sessionsQ.isLoading && <LoadingState label="加载设备列表..." />}
        {sessionsQ.error && <div className="text-danger text-sm">加载失败：{(sessionsQ.error as Error).message}</div>}

        {!sessionsQ.isLoading && sessions.length === 0 && (
          <EmptyState title="没有活跃设备" hint="该用户当前没有任何已登录且未过期的会话。" />
        )}

        {sessions.length > 0 && (
          <>
            <div className="flex justify-end">
              <button
                className="btn-danger btn-sm"
                disabled={revokeAll.isPending}
                onClick={async () => {
                  if (await confirm({ message: '将该用户所有设备下线？', detail: '所有设备下次请求都会被踢回登录页，需要重新输 PIN。', danger: true })) {
                    revokeAll.mutate();
                  }
                }}
              >
                全部下线
              </button>
            </div>
            <div className="space-y-2">
              {sessions.map((s) => (
                <SessionRow
                  key={s.token}
                  session={s}
                  onRevoke={(token) =>
                    confirm({ message: '踢这台设备下线？', detail: deviceLabel(s), danger: true }).then((ok) => {
                      if (ok) revokeOne.mutate(token);
                    })
                  }
                  onSaveNote={(note) => saveNote.mutate({ token: s.token, note })}
                  savingNote={saveNote.isPending}
                />
              ))}
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}

// deviceLabel returns the most recognizable name for a session: note first,
// then the OS device name, then a UA snippet. Used in both the row and the
// revoke-confirm dialog.
function deviceLabel(s: UserSession): string {
  if (s.note) return s.note;
  if (s.device_name) return s.device_name;
  // UA fallback: trim to a short, readable chunk.
  const ua = s.user_agent ?? '';
  return ua.length > 60 ? ua.slice(0, 60) + '…' : (ua || `会话 ${s.token_prefix}`);
}

function SessionRow({
  session,
  onRevoke,
  onSaveNote,
  savingNote,
}: {
  session: UserSession;
  onRevoke: (token: string) => void;
  onSaveNote: (note: string) => void;
  savingNote: boolean;
}) {
  const [note, setNote] = useState(session.note ?? '');
  const dirty = note !== (session.note ?? '');

  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border bg-card-2 p-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium text-txt">{session.device_name || deviceLabel(session)}</span>
            {session.note && <Tag color="#fbbf24">{session.note}</Tag>}
          </div>
          <div className="mt-0.5 truncate text-xs text-muted" title={session.user_agent}>
            {session.user_agent || '无 User-Agent'}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-muted">
            <span>登录 {relativeTime(session.created_at)}</span>
            <span>最近活跃 {relativeTime(session.last_seen_at)}</span>
            <span>token {session.token_prefix}…</span>
          </div>
        </div>
        <button
          className="btn-danger btn-sm shrink-0"
          onClick={() => onRevoke(session.token)}
        >
          下线
        </button>
      </div>
      <div className="flex items-center gap-2">
        <input
          className="input flex-1 text-sm"
          placeholder="为这台设备加备注（如「客厅 iPad」）"
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
        <button
          className="btn-secondary btn-sm shrink-0"
          disabled={!dirty || savingNote}
          onClick={() => onSaveNote(note)}
        >
          保存备注
        </button>
      </div>
    </div>
  );
}

// StorageWhitelistSection renders the user's storage-source whitelist (防呆)
// as a checkbox list. Semantics: an EMPTY list means default-deny (the user
// is allowed NO sources — any content that lives on a storage source is
// refused at grant time; see backend admin_storage_gate.go). The admin must
// grant at least one source before the user can stream imported content.
// The whole list is replaced on every toggle via setStorageWhitelist (PUT).
// The catalog comes from useStorageSources (warmed at the app root).
function StorageWhitelistSection({ userId, current }: { userId: number; current: number[] }) {
  const sourcesQ = useStorageSources();
  const qc = useQueryClient();
  const toast = useToast();
  const selected = new Set(current);
  const sources = sourcesQ.data ?? [];

  // Optimistic update: patch the cached ['users'] entry's storage_source_access
  // BEFORE the PUT resolves, so a rapid second toggle reads the just-clicked
  // baseline instead of the stale server snapshot (which would otherwise lose
  // the first toggle to last-write-wins). onMutate returns the previous cache
  // so onError can roll back.
  const mut = useMutation({
    mutationFn: (ids: number[]) => api.setStorageWhitelist(userId, ids),
    onMutate: async (nextIds: number[]) => {
      await qc.cancelQueries({ queryKey: ['users'] });
      const prev = qc.getQueryData<User[]>(['users']);
      qc.setQueryData<User[]>(['users'], (old) =>
        (old ?? []).map((u) => (u.id === userId ? { ...u, storage_source_access: nextIds } : u)),
      );
      return { prev };
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (e, _ids, ctx) => {
      if (ctx?.prev) qc.setQueryData(['users'], ctx.prev);
      toast.error((e as Error).message);
    },
  });

  const toggle = (id: number, on: boolean) => {
    const next = on ? [...selected, id] : [...selected].filter((x) => x !== id);
    mut.mutate(next);
  };

  return (
    <section className="mb-6">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="flex items-center gap-1.5 text-sm font-semibold text-txt">
          <Database size={14} className="text-muted" />
          允许的存储源 ({selected.size}/{sources.length})
        </h3>
        {selected.size > 0 && (
          <button className="btn-danger btn-sm" onClick={() => mut.mutate([])} disabled={mut.isPending}>
            清空（全拒）
          </button>
        )}
      </div>
      <p className="mb-2 text-xs text-muted">
        防呆：勾选后该用户只能访问这些源的内容。<strong>空 = 一个都不允许</strong>（必须勾选至少一个源，用户才能播放任何内容）。
      </p>
      {sources.length === 0 ? (
        <p className="rounded-lg border border-border bg-card-2 px-3 py-2 text-xs text-muted">
          尚未配置存储源（在「系统设置」新增）。
        </p>
      ) : (
        <div className="max-h-48 space-y-1 overflow-auto">
          {sources.map((s) => (
            <label key={s.id} className="flex items-center gap-2 rounded-lg border border-border bg-card-2 px-3 py-1.5 text-sm">
              <input
                type="checkbox"
                checked={selected.has(s.id!)}
                onChange={(e) => toggle(s.id!, e.target.checked)}
                className="h-4 w-4 accent-primary"
              />
              <span className="flex-1 text-txt">
                {s.name}
                {s.is_default && <span className="ml-1 text-[10px] text-primary">默认</span>}
              </span>
              <span className="text-[10px] uppercase text-muted">{s.type}</span>
            </label>
          ))}
        </div>
      )}
    </section>
  );
}
