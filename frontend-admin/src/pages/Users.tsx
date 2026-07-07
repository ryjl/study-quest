import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import type { User } from '../lib/types';
import { Modal, LoadingState, EmptyState, Drawer, Tag } from '../components/ui';
import { ImageUpload } from '../components/inputs';
import { relativeTime } from '../lib/format';

// Compact "Xh Ym" / "Ym" formatter for accumulated watch minutes.
function formatMinutes(min: number | undefined): string {
  const m = Math.max(0, Math.floor(min ?? 0));
  if (m < 60) return `${m} 分`;
  const h = Math.floor(m / 60);
  const rem = m % 60;
  return rem === 0 ? `${h} 时` : `${h} 时 ${rem} 分`;
}
import { useToast, useConfirm } from '../lib/toast';

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
                    <td className="p-3 text-muted">{formatMinutes(u.watch_minutes)}</td>
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
          />
        );
      })()}
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
}: {
  user: User;
  onClose: () => void;
  onToggleCourse: (courseId: number, grant: boolean) => void;
  onBulk: (action: 'grant_all' | 'revoke_all') => void;
}) {
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const ledgerQ = useQuery({ queryKey: ['ledger', user.id], queryFn: () => api.userLedger(user.id, 10) });
  const badgesQ = useQuery({ queryKey: ['user-badges', user.id], queryFn: () => api.userBadges(user.id) });

  const access = new Set(user.course_access ?? []);
  const courses = coursesQ.data ?? [];
  const ledger = ledgerQ.data ?? [];
  const badges = badgesQ.data ?? [];
  const unlocked = badges.filter((b) => b.unlocked);

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
