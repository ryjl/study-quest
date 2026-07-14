import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api } from '../lib/api';
import { Modal } from '../components/ui';
import { useToast } from '../lib/toast';
import { useDeleteConfirm } from '../lib/useDeleteConfirm';
import { useStorageSources, useInvalidateStorageSources } from '../lib/useStorageSources';
import type { StorageSource } from '../lib/types';

// StorageSourcesSection is the multi-source management card on the Settings
// page. Renders the list of configured sources with edit/delete/test-connection
// actions, plus an "add source" button that opens a modal form.
//
// A source row carries: name, type (alist/webdav), url, username, password,
// token (alist only), is_default. The modal form is shared between create and
// edit (edit pre-fills from the clicked row).
export function StorageSourcesSection() {
  const sourcesQ = useStorageSources();
  const invalidate = useInvalidateStorageSources();
  const toast = useToast();
  const [editing, setEditing] = useState<StorageSource | null>(null);
  const [open, setOpen] = useState(false);

  const del = useDeleteConfirm({ mutationFn: api.deleteStorageSource, noun: '存储源', onDeleted: invalidate });

  const createMut = useMutation({
    mutationFn: (body: StorageSource) => api.createStorageSource(body),
    onSuccess: () => { toast.success('已创建'); invalidate(); setOpen(false); },
    onError: (e) => toast.error((e as Error).message),
  });
  const updateMut = useMutation({
    mutationFn: ({ id, body }: { id: number; body: StorageSource }) => api.updateStorageSource(id, body),
    onSuccess: () => { toast.success('已保存'); invalidate(); setOpen(false); },
    onError: (e) => toast.error((e as Error).message),
  });

  const openCreate = () => { setEditing(null); setOpen(true); };
  const openEdit = (s: StorageSource) => { setEditing(s); setOpen(true); };

  return (
    <div className="card">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h2 className="text-base font-bold text-txt">存储源管理</h2>
          <p className="mt-0.5 text-xs text-muted">多存储源：内容跟源走，用户白名单防呆。空 = 不限制。</p>
        </div>
        <button className="btn-primary btn-sm" onClick={openCreate}>+ 新增存储源</button>
      </div>

      {sourcesQ.isLoading ? (
        <p className="text-sm text-muted">加载中…</p>
      ) : sourcesQ.data && sourcesQ.data.length > 0 ? (
        <div className="space-y-2">
          {sourcesQ.data.map((s) => (
            <SourceRow key={s.id} source={s} onEdit={() => openEdit(s)} onDelete={() => del.confirmAndDelete(s.id!, `确认删除存储源「${s.name}」？`, '已绑定该源的内容会回退到全局存储配置。')} />
          ))}
        </div>
      ) : (
        <p className="text-sm text-muted">尚未配置存储源。新增后导入时可选择，旧内容走下方全局配置兼容回退。</p>
      )}

      {open && (
        <SourceModal
          source={editing}
          pending={createMut.isPending || updateMut.isPending}
          onCancel={() => setOpen(false)}
          onSubmit={(body) => {
            if (editing?.id) updateMut.mutate({ id: editing.id, body });
            else createMut.mutate(body);
          }}
        />
      )}
    </div>
  );
}

function SourceRow({ source, onEdit, onDelete }: { source: StorageSource; onEdit: () => void; onDelete: () => void }) {
  const toast = useToast();
  const pingMut = useMutation({
    mutationFn: () => api.pingStorageSource(source.id!),
    onSuccess: (d) => toast.success(d.message || '连接成功'),
    onError: (e) => toast.error((e as Error).message),
  });
  return (
    <div className="flex items-center gap-3 rounded-lg border border-border bg-card-2 px-3 py-2">
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold text-txt">{source.name}</span>
          {source.is_default && <span className="rounded bg-primary/15 px-1.5 py-0.5 text-[10px] text-primary">默认</span>}
          <span className="rounded bg-border px-1.5 py-0.5 text-[10px] text-muted uppercase">{source.type}</span>
        </div>
        <div className="truncate text-xs text-muted font-mono">{source.url}</div>
      </div>
      <button className="btn-secondary btn-sm" onClick={() => pingMut.mutate()} disabled={pingMut.isPending}>
        {pingMut.isPending ? '测试中…' : '🔌 测试'}
      </button>
      <button className="btn-ghost btn-sm" onClick={onEdit}>编辑</button>
      <button className="btn-danger btn-sm" onClick={onDelete}>删除</button>
    </div>
  );
}

function SourceModal({ source, pending, onCancel, onSubmit }: {
  source: StorageSource | null;
  pending: boolean;
  onCancel: () => void;
  onSubmit: (body: StorageSource) => void;
}) {
  const [name, setName] = useState(source?.name ?? '');
  const [type, setType] = useState(source?.type ?? 'alist');
  const [url, setUrl] = useState(source?.url ?? '');
  const [username, setUsername] = useState(source?.username ?? '');
  const [password, setPassword] = useState(source?.password ?? '');
  const [token, setToken] = useState(source?.token ?? '');
  const [isDefault, setIsDefault] = useState(source?.is_default ?? false);

  const submit = () => {
    if (!name.trim() || !url.trim()) return;
    onSubmit({ name: name.trim(), type, url: url.trim(), username, password, token, is_default: isDefault });
  };

  return (
    <Modal open={true} onClose={onCancel} title={source ? '编辑存储源' : '新增存储源'} size="md">
      <div className="space-y-3">
        <div>
          <label className="mb-1 block text-xs text-muted">名称</label>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="如：家长追剧盘" />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="mb-1 block text-xs text-muted">类型</label>
            <select className="input" value={type} onChange={(e) => setType(e.target.value)}>
              <option value="alist">AList</option>
              <option value="webdav">WebDAV</option>
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">设为默认</label>
            <label className="flex h-[38px] items-center gap-2 text-sm text-txt">
              <input type="checkbox" checked={isDefault} onChange={(e) => setIsDefault(e.target.checked)} className="h-4 w-4 accent-primary" />
              <span className="text-xs text-muted">导入时的默认选项</span>
            </label>
          </div>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">服务地址</label>
          <input className="input font-mono" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="http://192.168.1.100:5244" />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="mb-1 block text-xs text-muted">用户名</label>
            <input className="input" value={username} onChange={(e) => setUsername(e.target.value)} />
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">密码</label>
            <input type="password" className="input" value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
        </div>
        {type === 'alist' && (
          <div>
            <label className="mb-1 block text-xs text-muted">AList Token</label>
            <input className="input font-mono" value={token} onChange={(e) => setToken(e.target.value)} placeholder="AList 授权令牌" />
          </div>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <button className="btn-secondary" onClick={onCancel}>取消</button>
          <button className="btn-primary" onClick={submit} disabled={pending || !name.trim() || !url.trim()}>
            {pending ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </Modal>
  );
}
