// Users — the user-and-authorization page. Renders the user table plus the
// three modal/drawer launch points (edit, detail drawer with access toggles,
// device sessions). The heavy lifting lives in siblings in this folder.
//
// ROLES is the closed role set the admin UI knows how to render (label + tag
// color). The backend role string is free, but only these four appear in the
// wild; an unknown role falls back to the first entry (student) via roleMeta.

import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { User } from '../../lib/types';
import { LoadingState, EmptyState, DropdownMenu } from '../../components/ui';
import { PageHeader } from '../../components/PageHeader';
import { useTypedMutation } from '../../lib/useTypedMutation';
import { useConfirm } from '../../lib/toast';
import { relativeTime, formatWatchTime } from '../../lib/format';
import { UserEditModal } from './UserEditModal';
import { UserDetailDrawer } from './UserDetailDrawer';
import { UserSessionsModal } from './UserSessionsModal';
import {
  Plus,
  KeyRound,
  Pencil,
  Trash2,
  Smartphone,
  Star,
  Bot,
  Users as UsersIcon,
} from 'lucide-react';

export const ROLES = [
  { key: 'student', label: '学生', color: '#60a5fa' },
  { key: 'admin', label: '管理员', color: '#6366f1' },
];

// roleMeta resolves a role key to its {label,color}, defaulting to student.
// Shared by the users table, the edit modal's role select, and the drawer's
// summary card.
export function roleMeta(role: string) {
  return ROLES.find((r) => r.key === role) ?? ROLES[0];
}

export function Users() {
  const navigate = useNavigate();
  const qc = useQueryClient();
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

  const delMut = useTypedMutation({
    mutationFn: api.deleteUser,
    successMsg: '用户已删除',
    invalidateKeys: [['users']],
    errorMsg: '删除失败',
  });

  return (
    <div>
      <PageHeader
        title="用户与授权"
        actions={
          <div className="flex gap-2">
            {/* AI 工作台入口:用户与授权(CRUD/授权)和学生 AI 工作台(围绕学生的 AI 数据)职责分离。 */}
            <button className="btn-secondary" onClick={() => navigate('/admin/ai/students')} title="进入学生 AI 工作台">
              <Bot size={14} /> AI 工作台
            </button>
            <button className="btn-primary" onClick={() => setCreating(true)}>
              <Plus size={14} /> 新增用户
            </button>
          </div>
        }
      />

      {usersQ.isLoading ? (
        <LoadingState />
      ) : users.length === 0 ? (
        <EmptyState icon={<UsersIcon size={32} />} title="暂无用户" hint="创建第一个学生账号开始使用" />
      ) : (
        // NOTE: no overflow-hidden on this wrapper — it clips the row action
        // DropdownMenu (⋯) which drops below the table, hiding 设备管理/编辑/
        // 删除 on the last rows. The border + bg-card carry the card look.
        <div className="rounded-lg border border-border/60 bg-card">
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
                          <img
                            src={u.avatar_url}
                            alt=""
                            className="h-8 w-8 rounded-full object-cover"
                            onError={(e) => ((e.target as HTMLImageElement).style.opacity = '0.3')}
                          />
                        ) : (
                          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-card-2 text-xs font-medium">
                            {u.nickname.slice(0, 1)}
                          </div>
                        )}
                        <span className="font-medium text-txt">{u.nickname}</span>
                      </div>
                    </td>
                    <td className="px-4 py-2.5">
                      <span
                        className="rounded px-2 py-0.5 text-xs font-medium"
                        style={{ backgroundColor: `${rm.color}1a`, color: rm.color }}
                      >
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
                    <td
                      className="px-4 py-2.5 tabular-nums text-muted"
                      title={u.last_active_at ? new Date(u.last_active_at).toLocaleString() : '从未学习'}
                    >
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
                                if (
                                  await confirm({
                                    message: `删除用户「${u.nickname}」？`,
                                    detail: '将一并删除其学习进度、积分与徽章记录。',
                                    danger: true,
                                  })
                                )
                                  delMut.mutate(u.id);
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

      {detailForId != null &&
        (() => {
          // Look up the live user so access toggles reflect immediately.
          const detailUser = users.find((u) => u.id === detailForId);
          if (!detailUser) return null; // user deleted → close drawer
          // The drawer is now self-contained: it manages its own staged draft
          // state and calls the access API directly on save (no per-toggle
          // callbacks from the parent).
          return <UserDetailDrawer user={detailUser} onClose={() => setDetailForId(null)} />;
        })()}

      {sessionsForId != null && (
        <UserSessionsModal userId={sessionsForId} onClose={() => setSessionsForId(null)} />
      )}
    </div>
  );
}
