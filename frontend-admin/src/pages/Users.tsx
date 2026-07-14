import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { User, UserSession, ReadingTargetType } from '../lib/types';
import { Modal, LoadingState, EmptyState, Drawer, Tag } from '../components/ui';
import { ImageUpload } from '../components/inputs';
import { UserCourseUnlockRow } from '../components/UserCourseUnlockRow';
import { relativeTime } from '../lib/format';

// Compact watch-time formatter. Prefers raw seconds (sub-minute precision) so
// a user who watched e.g. 40 seconds doesn't show a misleading "0 分". Falls
// back to whole minutes only when seconds aren't available.
function formatWatchTime(seconds?: number, minutes?: number): string {
  if (seconds !== undefined && seconds > 0) {
    const s = Math.floor(seconds);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const rem = s % 60;
    if (h > 0) return rem === 0 ? (m === 0 ? `${h} 时` : `${h} 时 ${m} 分`) : `${h} 时 ${m} 分`;
    if (m > 0) return rem === 0 ? `${m} 分` : `${m} 分 ${rem} 秒`;
    return `${rem} 秒`;
  }
  const m = Math.max(0, Math.floor(minutes ?? 0));
  if (m < 60) return `${m} 分`;
  const h = Math.floor(m / 60);
  const rem = m % 60;
  return rem === 0 ? `${h} 时` : `${h} 时 ${rem} 分`;
}
import { useToast, useConfirm } from '../lib/toast';
import { useStorageSources } from '../lib/useStorageSources';

const ROLES = [
  { key: 'student', label: '学生', color: '#60a5fa' },
  { key: 'teen', label: '青少年', color: '#fbbf24' },
  { key: 'parent', label: '家长', color: '#34d399' },
  { key: 'admin', label: '管理员', color: '#a78bfa' },
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

  const bulkMut = useMutation({
    mutationFn: ({ id, action }: { id: number; action: 'grant_all' | 'revoke_all' }) => api.bulkAccess(id, action),
    onSuccess: () => {
      toast.success('授权已更新');
      qc.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const accessMut = useMutation({
    mutationFn: ({ userId, courseId, grant }: { userId: number; courseId: number; grant: boolean }) =>
      grant ? api.grantAccess(userId, courseId) : api.revokeAccess(userId, courseId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
    onError: (e) => toast.error((e as Error).message),
  });
  // Reading Room access — one mutation for all three target types.
  const readingAccessMut = useMutation({
    mutationFn: ({ userId, targetType, targetId, grant }: { userId: number; targetType: ReadingTargetType; targetId: number; grant: boolean }) =>
      grant ? api.grantReadingAccess(userId, targetType, targetId) : api.revokeReadingAccess(userId, targetType, targetId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
    onError: (e) => toast.error((e as Error).message),
  });
  const readingBulkMut = useMutation({
    mutationFn: ({ id, action }: { id: number; action: 'grant_all' | 'revoke_all' }) => api.bulkReadingAccess(id, action),
    onSuccess: () => { toast.success('阅读室授权已更新'); qc.invalidateQueries({ queryKey: ['users'] }); },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <div>
      <div className="mb-6 flex items-center justify-between border-b border-border pb-4">
        <h1 className="text-2xl font-bold text-txt">用户与授权</h1>
        <button className="btn-primary" onClick={() => setCreating(true)}>
          + 新增用户
        </button>
      </div>

      {usersQ.isLoading ? (
        <LoadingState />
      ) : users.length === 0 ? (
        <EmptyState icon="👥" title="暂无用户" hint="创建第一个学生账号开始使用" />
      ) : (
        <div className="card !p-0 overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted">
                <th className="p-3">用户</th>
                <th className="p-3">角色</th>
                <th className="p-3">授权 / 完课</th>
                <th className="p-3">学习时长</th>
                <th className="p-3">积分</th>
                <th className="p-3">徽章</th>
                <th className="p-3">最后活跃</th>
                <th className="p-3 text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const rm = roleMeta(u.role);
                const access = u.course_access ?? [];
                return (
                  <tr key={u.id} className="border-b border-border/50 text-sm hover:bg-card-2/40">
                    <td className="p-3">
                      <div className="flex items-center gap-2.5">
                        {u.avatar_url ? (
                          <img src={u.avatar_url} alt="" className="h-9 w-9 rounded-full object-cover" onError={(e) => ((e.target as HTMLImageElement).style.opacity = '0.3')} />
                        ) : (
                          <div className="flex h-9 w-9 items-center justify-center rounded-full bg-card-2 text-sm">{u.nickname.slice(0, 1)}</div>
                        )}
                        <span className="font-medium text-txt">{u.nickname}</span>
                      </div>
                    </td>
                    <td className="p-3">
                      <span className="rounded px-2 py-0.5 text-xs font-semibold" style={{ backgroundColor: `${rm.color}20`, color: rm.color }}>
                        {rm.label}
                      </span>
                    </td>
                    <td className="p-3">
                      <div className="text-txt">
                        {access.length} <span className="text-muted">门</span>
                      </div>
                      <div className="text-xs text-muted" title="已完成课时 / 可访问总课时">
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
                            <span className="text-[10px] text-muted">等 {access.length} 门</span>
                          )}
                        </div>
                      )}
                    </td>
                    <td className="p-3 text-muted">{formatWatchTime(u.watch_seconds, u.watch_minutes)}</td>
                    <td className="p-3">
                      <span className="text-warn">⭐ {u.current_points ?? 0}</span>
                    </td>
                    <td className="p-3">
                      <span className="text-txt">{u.unlocked_badges ?? 0}</span>
                      <span className="text-muted"> / {u.total_badges ?? 0}</span>
                    </td>
                    <td className="p-3 text-muted" title={u.last_active_at ? new Date(u.last_active_at).toLocaleString() : '从未学习'}>
                      {u.last_active_at ? relativeTime(u.last_active_at) : '—'}
                    </td>
                    <td className="p-3">
                      <div className="flex justify-end gap-1.5">
                        <button className="btn-ghost btn-sm" onClick={() => setDetailForId(u.id)}>
                          授权
                        </button>
                        <button className="btn-ghost btn-sm" onClick={() => setSessionsForId(u.id)} title="查看/管理该用户登录的设备">
                          设备
                        </button>
                        <button className="btn-ghost btn-sm" onClick={() => setEditing(u)}>
                          编辑
                        </button>
                        <button
                          className="btn-danger btn-sm"
                          onClick={async () => {
                            if (await confirm({ message: `删除用户「${u.nickname}」？`, detail: '将一并删除其学习进度、积分与徽章记录。', danger: true })) delMut.mutate(u.id);
                          }}
                        >
                          删除
                        </button>
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
        return (
          <UserDetailDrawer
            user={detailUser}
            onClose={() => setDetailForId(null)}
            onToggleCourse={(courseId, grant) => accessMut.mutate({ userId: detailUser.id, courseId, grant })}
            onBulk={(action) => bulkMut.mutate({ id: detailUser.id, action })}
            onToggleReading={(targetType, targetId, grant) => readingAccessMut.mutate({ userId: detailUser.id, targetType, targetId, grant })}
            onReadingBulk={(action) => readingBulkMut.mutate({ id: detailUser.id, action })}
          />
        );
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

function UserDetailDrawer({
  user,
  onClose,
  onToggleCourse,
  onBulk,
  onToggleReading,
  onReadingBulk,
}: {
  user: User;
  onClose: () => void;
  onToggleCourse: (courseId: number, grant: boolean) => void;
  onBulk: (action: 'grant_all' | 'revoke_all') => void;
  onToggleReading: (targetType: ReadingTargetType, targetId: number, grant: boolean) => void;
  onReadingBulk: (action: 'grant_all' | 'revoke_all') => void;
}) {
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const ledgerQ = useQuery({ queryKey: ['ledger', user.id], queryFn: () => api.userLedger(user.id, 10) });
  const badgesQ = useQuery({ queryKey: ['user-badges', user.id], queryFn: () => api.userBadges(user.id) });
  // Reading Room catalogs — for the checkbox lists.
  const readingSeriesQ = useQuery({ queryKey: ['reading-series'], queryFn: api.listReadingSeries });
  const readingBooksQ = useQuery({ queryKey: ['reading-books'], queryFn: api.listReadingBooks });
  const readingArticlesQ = useQuery({ queryKey: ['reading-articles'], queryFn: api.listReadingArticles });

  const access = new Set(user.course_access ?? []);
  const courses = coursesQ.data ?? [];
  const seriesAccess = new Set(user.reading_series_access ?? []);
  const bookAccess = new Set(user.reading_book_access ?? []);
  const articleAccess = new Set(user.reading_article_access ?? []);
  const readingSeries = readingSeriesQ.data ?? [];
  const readingBooks = readingBooksQ.data ?? [];
  const readingArticles = readingArticlesQ.data ?? [];
  const ledger = ledgerQ.data ?? [];
  const badges = badgesQ.data ?? [];
  const unlocked = badges.filter((b) => b.unlocked);
  // Granted course ids in catalog order (so the unlock rows match the checkbox
  // list above, not the raw access array order).
  const granted = courses.filter((c) => access.has(c.id)).map((c) => c.id);

  return (
    <Drawer open onClose={onClose} title={`${user.nickname} · 课程授权`} width="lg">
      <section className="mb-6">
        <div className="mb-2 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-txt">课程授权 ({access.size}/{courses.length})</h3>
          <div className="flex gap-1.5">
            <button className="btn-secondary btn-sm" onClick={() => onBulk('grant_all')}>
              全部授权
            </button>
            <button className="btn-danger btn-sm" onClick={() => onBulk('revoke_all')}>
              全部撤销
            </button>
          </div>
        </div>
        <div className="max-h-60 space-y-1 overflow-auto">
          {courses.map((c) => (
            <label key={c.id} className="flex items-center gap-2 rounded-lg border border-border bg-card-2 px-3 py-1.5 text-sm">
              <input
                type="checkbox"
                checked={access.has(c.id)}
                onChange={(e) => onToggleCourse(c.id, e.target.checked)}
                className="h-4 w-4 accent-primary"
              />
              <span className="flex-1 text-txt">{c.title}</span>
            </label>
          ))}
        </div>
      </section>

      {/* Reading Room access — series / books / articles, mirroring the course
          checkbox section above. Three collapsible sub-lists, one bulk bar. */}
      <section className="mb-6">
        <div className="mb-2 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-txt">📖 阅读室授权</h3>
          <div className="flex gap-1.5">
            <button className="btn-secondary btn-sm" onClick={() => onReadingBulk('grant_all')}>全部授权</button>
            <button className="btn-danger btn-sm" onClick={() => onReadingBulk('revoke_all')}>全部撤销</button>
          </div>
        </div>

        {readingSeries.length > 0 && (
          <div className="mb-2">
            <p className="mb-1 text-xs text-muted">系列 ({seriesAccess.size}/{readingSeries.length})</p>
            <div className="max-h-40 space-y-1 overflow-auto">
              {readingSeries.map((s) => (
                <label key={s.id} className="flex items-center gap-2 rounded-lg border border-border bg-card-2 px-3 py-1.5 text-sm">
                  <input type="checkbox" checked={seriesAccess.has(s.id)} onChange={(e) => onToggleReading('series', s.id, e.target.checked)} className="h-4 w-4 accent-primary" />
                  <span className="flex-1 text-txt">{s.title}</span>
                  <span className="text-xs text-muted">{s.book_count + s.article_count} 项</span>
                </label>
              ))}
            </div>
          </div>
        )}

        {readingBooks.length > 0 && (
          <div className="mb-2">
            <p className="mb-1 text-xs text-muted">书籍 PDF ({bookAccess.size}/{readingBooks.length})</p>
            <div className="max-h-40 space-y-1 overflow-auto">
              {readingBooks.map((b) => (
                <label key={b.id} className="flex items-center gap-2 rounded-lg border border-border bg-card-2 px-3 py-1.5 text-sm">
                  <input type="checkbox" checked={bookAccess.has(b.id)} onChange={(e) => onToggleReading('book', b.id, e.target.checked)} className="h-4 w-4 accent-primary" />
                  <span className="flex-1 text-txt">📕 {b.title}</span>
                </label>
              ))}
            </div>
          </div>
        )}

        {readingArticles.length > 0 && (
          <div className="mb-2">
            <p className="mb-1 text-xs text-muted">文章 ({articleAccess.size}/{readingArticles.length})</p>
            <div className="max-h-40 space-y-1 overflow-auto">
              {readingArticles.map((a) => (
                <label key={a.id} className="flex items-center gap-2 rounded-lg border border-border bg-card-2 px-3 py-1.5 text-sm">
                  <input type="checkbox" checked={articleAccess.has(a.id)} onChange={(e) => onToggleReading('article', a.id, e.target.checked)} className="h-4 w-4 accent-primary" />
                  <span className="flex-1 text-txt">🌐 {a.title}</span>
                </label>
              ))}
            </div>
          </div>
        )}

        {readingSeries.length === 0 && readingBooks.length === 0 && readingArticles.length === 0 && (
          <p className="text-sm text-muted">阅读室还没有内容，请先到阅读室页面添加。</p>
        )}
      </section>

      {/* Storage-source whitelist (防呆). Empty = unrestricted. Whole-list
          replace on every toggle via setStorageWhitelist. */}
      <StorageWhitelistSection userId={user.id} current={user.storage_source_access ?? []} />

      {/* Per-course unlock controls — only for granted courses. Lets the admin
          manually bump the water level, cherry-pick episodes (selected mode),
          or override the strategy for this specific student. */}
      {granted.length > 0 && (
        <section className="mb-6">
          <h3 className="mb-2 text-sm font-semibold text-txt">🔓 解锁节奏（已授权课程）</h3>
          <div className="space-y-1.5">
            {granted.map((cid) => {
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
        </section>
      )}

      <section className="mb-6">
        <h3 className="mb-2 text-sm font-semibold text-txt">积分流水（近 10 条）</h3>
        {ledger.length === 0 ? (
          <p className="text-sm text-muted">暂无记录</p>
        ) : (
          <div className="space-y-1">
            {ledger.map((l) => (
              <div key={l.id} className="flex items-center justify-between rounded-lg bg-card-2 px-3 py-1.5 text-sm">
                <span className="text-txt">{l.description || l.reason_type}</span>
                <span className={l.change_amount >= 0 ? 'text-good' : 'text-bad'}>
                  {l.change_amount >= 0 ? '+' : ''}
                  {l.change_amount}
                </span>
              </div>
            ))}
          </div>
        )}
      </section>

      <section>
        <h3 className="mb-2 text-sm font-semibold text-txt">已解锁徽章 ({unlocked.length})</h3>
        {unlocked.length === 0 ? (
          <p className="text-sm text-muted">暂无</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {unlocked.map((b) => (
              <Tag key={b.id} color="#fbbf24">
                🏅 {b.title}
              </Tag>
            ))}
          </div>
        )}
      </section>
    </Drawer>
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

// StorageWhitelistSection renders the user's storage-source whitelist (防呆) as
// a checkbox list. Empty selection = unrestricted (backward compatible). The
// whole list is replaced on every toggle via setStorageWhitelist (PUT). The
// catalog comes from useStorageSources (warmed at the app root).
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
        <h3 className="text-sm font-semibold text-txt">
          💾 允许的存储源 ({selected.size}/{sources.length})
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
