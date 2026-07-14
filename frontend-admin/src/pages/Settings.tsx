import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { LoadingState } from '../components/ui';
import { useToast } from '../lib/toast';
import { StorageSourcesSection } from './StorageSourcesSection';

export function Settings() {
  const qc = useQueryClient();
  const toast = useToast();
  const settingsQ = useQuery({ queryKey: ['settings'], queryFn: api.getSettings });

  const [storageType, setStorageType] = useState('alist');
  const [storageUrl, setStorageUrl] = useState('');
  const [storageUsername, setStorageUsername] = useState('');
  const [storagePassword, setStoragePassword] = useState('');
  const [storageToken, setStorageToken] = useState('');
  const [adminPassword, setAdminPassword] = useState('');
  const [pingResult, setPingResult] = useState<{ ok: boolean; msg: string } | null>(null);

  useEffect(() => {
    const s = settingsQ.data;
    if (s) {
      setStorageType(s.storage_type || 'alist');
      setStorageUrl(s.storage_url || '');
      setStorageUsername(s.storage_username || '');
      setStoragePassword(s.storage_password || '');
      setStorageToken(s.storage_token || '');
    }
  }, [settingsQ.data]);

  const saveMut = useMutation({
    mutationFn: () =>
      api.updateSettings({
        storage_type: storageType,
        storage_url: storageUrl,
        storage_username: storageUsername,
        storage_password: storagePassword,
        storage_token: storageToken,
        admin_password: adminPassword || undefined,
      }),
    onSuccess: (d) => {
      toast.success(d.message || '设置已保存');
      setAdminPassword('');
      qc.invalidateQueries({ queryKey: ['settings'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const pingMut = useMutation({
    mutationFn: () => api.pingStorage({ storage_type: storageType, storage_url: storageUrl, storage_username: storageUsername, storage_password: storagePassword, storage_token: storageToken }),
    onSuccess: (d) => setPingResult({ ok: true, msg: d.message }),
    onError: (e) => setPingResult({ ok: false, msg: (e as Error).message }),
  });

  const probeMut = useMutation({
    mutationFn: api.scanMissingDurations,
    onSuccess: (d) => toast.info(d.message),
    onError: (e) => toast.error((e as Error).message),
  });

  if (settingsQ.isLoading) return <LoadingState />;

  return (
    <div>
      <h1 className="mb-6 border-b border-border pb-4 text-2xl font-bold text-txt">系统设置</h1>

      <div className="mb-5">
        <StorageSourcesSection />
      </div>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <div className="card">
          <h2 className="mb-1 text-base font-bold text-txt">全局存储配置（兼容回退）</h2>
          <p className="mb-3 text-xs text-muted">未指定存储源的内容走此配置。迁移到多源后可忽略；删除前请确认所有内容已绑定存储源。</p>
          <div className="space-y-4">
            <div>
              <label className="mb-1 block text-xs text-muted">存储类型</label>
              <select className="input" value={storageType} onChange={(e) => setStorageType(e.target.value)}>
                <option value="alist">AList</option>
                <option value="webdav">WebDAV</option>
              </select>
            </div>
            <div>
              <label className="mb-1 block text-xs text-muted">服务地址</label>
              <input className="input font-mono" value={storageUrl} onChange={(e) => setStorageUrl(e.target.value)} placeholder="http://192.168.1.100:5244" />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="mb-1 block text-xs text-muted">用户名</label>
                <input className="input" value={storageUsername} onChange={(e) => setStorageUsername(e.target.value)} />
              </div>
              <div>
                <label className="mb-1 block text-xs text-muted">密码</label>
                <input type="password" className="input" value={storagePassword} onChange={(e) => setStoragePassword(e.target.value)} />
              </div>
            </div>
            {storageType === 'alist' && (
              <div>
                <label className="mb-1 block text-xs text-muted">AList Token</label>
                <input className="input font-mono" value={storageToken} onChange={(e) => setStorageToken(e.target.value)} placeholder="AList 授权令牌" />
              </div>
            )}

            {pingResult && (
              <div className={`rounded-lg border px-3 py-2 text-sm ${pingResult.ok ? 'border-good/40 bg-good/10 text-good' : 'border-bad/40 bg-bad/10 text-bad'}`}>
                {pingResult.ok ? '✓ ' : '✕ '}
                {pingResult.msg}
              </div>
            )}

            <div className="flex gap-2">
              <button className="btn-secondary" onClick={() => pingMut.mutate()} disabled={pingMut.isPending}>
                {pingMut.isPending ? '测试中...' : '🔌 测试连接'}
              </button>
              <button className="btn-primary flex-1" onClick={() => saveMut.mutate()} disabled={saveMut.isPending}>
                {saveMut.isPending ? '保存中...' : '保存设置'}
              </button>
            </div>
          </div>
        </div>

        <div className="space-y-5">
          <div className="card">
            <h2 className="mb-4 text-base font-bold text-txt">管理员密码</h2>
            <label className="mb-1 block text-xs text-muted">重置密码（留空则不修改）</label>
            <input type="password" className="input" value={adminPassword} onChange={(e) => setAdminPassword(e.target.value)} placeholder="新密码" />
            <button className="btn-primary mt-3" onClick={() => saveMut.mutate()} disabled={saveMut.isPending || !adminPassword}>
              更新密码
            </button>
          </div>

          <div className="card">
            <h2 className="mb-2 text-base font-bold text-txt">视频时长探测</h2>
            <p className="mb-3 text-xs text-muted">使用 ffprobe 探测缺准时长的课时，串行限速约每集 4 秒。</p>
            <button className="btn-secondary" onClick={() => probeMut.mutate()} disabled={probeMut.isPending}>
              {probeMut.isPending ? '排队中...' : '📡 扫描缺失时长'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
