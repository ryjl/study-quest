// UserSessionsModal lists a user's active device sessions and lets the admin
// revoke one ("踢下线") or all ("全部下线"), and relabel a device with a
// freeform note (e.g. "客厅那台 iPad"). Display rule: note → device_name → UA
// snippet, so even a client that didn't send device_name is identifiable.

import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { UserSession } from '../../lib/types';
import { Modal, LoadingState, EmptyState, Tag } from '../../components/ui';
import { relativeTime } from '../../lib/format';
import { useToast, useConfirm } from '../../lib/toast';

export function UserSessionsModal({ userId, onClose }: { userId: number; onClose: () => void }) {
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
                  if (
                    await confirm({
                      message: '将该用户所有设备下线？',
                      detail: '所有设备下次请求都会被踢回登录页，需要重新输 PIN。',
                      danger: true,
                    })
                  ) {
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
  return ua.length > 60 ? ua.slice(0, 60) + '…' : ua || `会话 ${s.token_prefix}`;
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
        <button className="btn-danger btn-sm shrink-0" onClick={() => onRevoke(session.token)}>
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
