import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Package } from 'lucide-react';
import { api } from '../lib/api';
import type { AppRelease } from '../lib/types';
import { formatFileSize } from '../lib/format';
import { Modal, LoadingState, EmptyState } from '../components/ui';
import { useToast, useConfirm } from '../lib/toast';
import { useTypedMutation } from '../lib/useTypedMutation';
import { PageHeader } from '../components/PageHeader';

// The closed set of ABIs the backend accepts. Mirrors SupportedABIs in
// release_handler.go — changing one without the other breaks upload.
const ABIS = ['arm64-v8a', 'armeabi-v7a', 'x86_64'] as const;

export function Releases() {
  const confirm = useConfirm();
  const [uploading, setUploading] = useState(false);
  const [editing, setEditing] = useState<AppRelease | null>(null);

  const releasesQ = useQuery({ queryKey: ['releases'], queryFn: api.listReleases });

  const deleteMut = useTypedMutation({
    mutationFn: (id: number) => api.deleteRelease(id),
    successMsg: '版本已删除',
    invalidateKeys: [['releases']],
    errorMsg: '删除失败',
  });

  const toggleActiveMut = useTypedMutation<{ id: number; isActive: boolean }, { status: string }>({
    mutationFn: async ({ id, isActive }) => {
      await api.updateRelease(id, { is_active: isActive });
      return { status: 'ok' };
    },
    invalidateKeys: [['releases']],
  });

  const onDelete = async (r: AppRelease) => {
    const ok = await confirm({
      message: `确认删除版本 ${r.version_name} (${r.abi})？`,
      detail: '将同时删除 APK 文件，已安装该版本的客户端不受影响。',
      danger: true,
    });
    if (ok) deleteMut.mutate(r.id);
  };

  if (releasesQ.isLoading) return <LoadingState />;
  const releases = releasesQ.data ?? [];

  return (
    <div>
      <PageHeader
        title="版本发布"
        breadcrumb={[{ label: '系统配置' }]}
        description="管理 App 版本与更新日志。"
        actions={
          <button className="btn-primary" onClick={() => setUploading(true)}>
            + 上传新版本
          </button>
        }
      />

      {releases.length === 0 ? (
        <EmptyState icon={<Package size={28} />} title="还没有发布版本" hint="构建 APK 后（make build-apk），在此上传以开启客户端自动更新。" />
      ) : (
        <div className="card overflow-hidden p-0">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted">
                <th className="px-4 py-3 font-medium">版本</th>
                <th className="px-4 py-3 font-medium">ABI</th>
                <th className="px-4 py-3 font-medium">大小</th>
                <th className="px-4 py-3 font-medium">强制更新</th>
                <th className="px-4 py-3 font-medium">状态</th>
                <th className="px-4 py-3 font-medium">发布时间</th>
                <th className="px-4 py-3 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {releases.map((r) => (
                <tr key={r.id} className="border-b border-border last:border-0 hover:bg-card-2/50">
                  <td className="px-4 py-3">
                    <div className="font-medium text-txt">{r.version_name}</div>
                    <div className="text-xs text-muted">code {r.version_code}</div>
                  </td>
                  <td className="px-4 py-3">
                    <code className="rounded bg-card-2 px-1.5 py-0.5 text-xs text-muted">{r.abi}</code>
                  </td>
                  <td className="px-4 py-3 text-muted">{formatFileSize(r.file_size)}</td>
                  <td className="px-4 py-3">{r.force_update ? <span className="text-warn">是</span> : <span className="text-muted">否</span>}</td>
                  <td className="px-4 py-3">
                    {r.is_active ? (
                      <span className="inline-flex items-center gap-1 text-good"><span className="h-1.5 w-1.5 rounded-full bg-good" />已发布</span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-bad"><span className="h-1.5 w-1.5 rounded-full bg-bad" />已撤回</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-muted">{new Date(r.created_at).toLocaleString()}</td>
                  <td className="px-4 py-3 text-right">
                    <button className="btn-ghost btn-sm" onClick={() => setEditing(r)}>编辑</button>
                    {r.is_active ? (
                      <button
                        className="btn-ghost btn-sm text-warn hover:bg-warn/10"
                        onClick={() => toggleActiveMut.mutate({ id: r.id, isActive: false })}
                        title="隐藏此版本，客户端将无法获取/下载"
                      >
                        撤回
                      </button>
                    ) : (
                      <button
                        className="btn-ghost btn-sm text-good hover:bg-good/10"
                        onClick={() => toggleActiveMut.mutate({ id: r.id, isActive: true })}
                      >
                        重新发布
                      </button>
                    )}
                    <button
                      className="btn-ghost btn-sm text-bad hover:bg-bad/10"
                      onClick={() => onDelete(r)}
                      disabled={deleteMut.isPending}
                    >
                      删除
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {uploading && <UploadModal onClose={() => setUploading(false)} />}
      {editing && <EditModal release={editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function UploadModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [file, setFile] = useState<File | null>(null);
  const [versionName, setVersionName] = useState('');
  const [versionCode, setVersionCode] = useState('');
  const [abi, setAbi] = useState<string>(ABIS[0]);
  const [forceUpdate, setForceUpdate] = useState(false);
  const [releaseNotes, setReleaseNotes] = useState('');

  const uploadMut = useMutation({
    mutationFn: () =>
      api.uploadRelease({
        file: file!,
        version_code: Number(versionCode),
        version_name: versionName.trim(),
        abi,
        force_update: forceUpdate,
        release_notes: releaseNotes,
      }),
    onSuccess: () => {
      toast.success('版本已上传');
      qc.invalidateQueries({ queryKey: ['releases'] });
      onClose();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '上传失败'),
  });

  const canSubmit = !!file && !!versionName.trim() && Number(versionCode) > 0;

  return (
    <Modal open onClose={onClose} title="上传新版本" size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (canSubmit) uploadMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">APK 文件</label>
          <input
            type="file"
            accept=".apk,application/vnd.android.package-archive"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            required
            className="block w-full text-sm text-muted file:mr-3 file:rounded-lg file:border-0 file:bg-primary file:px-3 file:py-2 file:text-white"
          />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="mb-1 block text-xs text-muted">版本名（如 1.2.0）</label>
            <input className="input" value={versionName} onChange={(e) => setVersionName(e.target.value)} placeholder="1.2.0" required autoFocus />
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">版本号 / code（整数，须递增）</label>
            <input className="input" type="number" min="1" value={versionCode} onChange={(e) => setVersionCode(e.target.value)} placeholder="12" required />
          </div>
        </div>
        <div>
          <label className="mb-1 block text-xs text-muted">ABI（CPU 架构）</label>
          <select className="input" value={abi} onChange={(e) => setAbi(e.target.value)}>
            {ABIS.map((a) => (
              <option key={a} value={a}>{a}</option>
            ))}
          </select>
          <p className="mt-1 text-xs text-muted">
            arm64-v8a = 主流 64 位真机；armeabi-v7a = 老 32 位；x86_64 = 模拟器。
          </p>
        </div>
        <label className="flex items-center gap-2 text-sm text-txt">
          <input type="checkbox" checked={forceUpdate} onChange={(e) => setForceUpdate(e.target.checked)} className="h-4 w-4" />
          强制更新（低版本必须升级才能使用）
        </label>
        <div>
          <label className="mb-1 block text-xs text-muted">更新说明</label>
          <textarea className="input min-h-[80px]" value={releaseNotes} onChange={(e) => setReleaseNotes(e.target.value)} placeholder="本次更新内容..." />
        </div>
        <button type="submit" className="btn-primary w-full" disabled={!canSubmit || uploadMut.isPending}>
          {uploadMut.isPending ? '上传中...' : '上传'}
        </button>
      </form>
    </Modal>
  );
}

function EditModal({ release, onClose }: { release: AppRelease; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [releaseNotes, setReleaseNotes] = useState(release.release_notes);
  const [forceUpdate, setForceUpdate] = useState(release.force_update);

  const saveMut = useMutation({
    mutationFn: () => api.updateRelease(release.id, { release_notes: releaseNotes, force_update: forceUpdate }),
    onSuccess: () => {
      toast.success('已更新');
      qc.invalidateQueries({ queryKey: ['releases'] });
      onClose();
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '保存失败'),
  });

  return (
    <Modal open onClose={onClose} title={`编辑 ${release.version_name} (${release.abi})`} size="md">
      <form onSubmit={(e) => { e.preventDefault(); saveMut.mutate(); }} className="space-y-4">
        <div>
          <label className="mb-1 block text-xs text-muted">更新说明</label>
          <textarea className="input min-h-[80px]" value={releaseNotes} onChange={(e) => setReleaseNotes(e.target.value)} />
        </div>
        <label className="flex items-center gap-2 text-sm text-txt">
          <input type="checkbox" checked={forceUpdate} onChange={(e) => setForceUpdate(e.target.checked)} className="h-4 w-4" />
          强制更新
        </label>
        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : '保存修改'}
        </button>
      </form>
    </Modal>
  );
}
