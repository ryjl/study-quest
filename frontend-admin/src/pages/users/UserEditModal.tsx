// UserEditModal — create/edit a single user account. Used by the Users table
// when the admin clicks 编辑用户 or 新增用户. On save it fires createUser or
// updateUser, toasts the result, and calls onSaved() so the parent can close
// the modal and invalidate the ['users'] list.

import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { User } from '../../lib/types';
import { Modal } from '../../components/ui';
import { ImageUpload } from '../../components/inputs';
import { useToast } from '../../lib/toast';
import { ROLES } from './Users';
import { isValidPin } from '../../lib/format';

export function UserEditModal({
  user,
  onClose,
  onSaved,
}: {
  user: User | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = !!user;
  const toast = useToast();
  const [nickname, setNickname] = useState(user?.nickname ?? '');
  const [avatarUrl, setAvatarUrl] = useState(user?.avatar_url ?? '');
  const [pin, setPin] = useState('');
  const [role, setRole] = useState(user?.role ?? 'student');
  const [grade, setGrade] = useState(user?.grade ?? '');

  const saveMut = useMutation({
    mutationFn: async () => {
      if (!nickname.trim()) throw new Error('请输入昵称');
      if (!isEdit && !isValidPin(pin)) throw new Error('PIN 必须为 6 位数字');
      if (isEdit) {
        const body: { nickname: string; avatar_url?: string; pin?: string; role: string; grade?: string } = {
          nickname: nickname.trim(),
          avatar_url: avatarUrl,
          role,
          grade,
        };
        if (pin) body.pin = pin;
        return api.updateUser(user!.id, body);
      }
      return api.createUser({ nickname: nickname.trim(), avatar_url: avatarUrl, pin, role, grade });
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
          <input
            className="input"
            value={nickname}
            onChange={(e) => setNickname(e.target.value)}
            required
            autoFocus
          />
        </div>
        <ImageUpload label="头像" value={avatarUrl} onChange={setAvatarUrl} />
        <div>
          <label className="mb-1 block text-xs text-muted">
            PIN 码 {isEdit && <span className="text-muted">（留空不修改）</span>}
          </label>
          <input
            type="password"
            className="input"
            value={pin}
            onChange={(e) => setPin(e.target.value)}
            placeholder="6 位数字"
            required={!isEdit}
            pattern="\d{6}"
            maxLength={6}
          />
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">年级（选填，如：四年级 / 初二）</label>
          <input
            className="input"
            value={grade}
            onChange={(e) => setGrade(e.target.value)}
            placeholder="留空则显示角色"
            maxLength={32}
          />
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
