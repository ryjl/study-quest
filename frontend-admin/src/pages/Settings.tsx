import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Wrench, Radio } from 'lucide-react';
import { api } from '../lib/api';
import { useToast } from '../lib/toast';
import { StorageSourcesSection } from './StorageSourcesSection';
import { AiProvidersSection } from './AiProvidersSection';
import { PageHeader } from '../components/PageHeader';
import { Section } from '../components/ui';

export function Settings() {
  const qc = useQueryClient();
  const toast = useToast();
  const [adminPassword, setAdminPassword] = useState('');

  const savePwdMut = useMutation({
    mutationFn: () => api.updateSettings({ admin_password: adminPassword }),
    onSuccess: (d) => {
      toast.success(d.message || '密码已更新');
      setAdminPassword('');
      qc.invalidateQueries({ queryKey: ['settings'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const probeMut = useMutation({
    mutationFn: api.scanMissingDurations,
    onSuccess: (d) => toast.info(d.message),
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <div>
      <PageHeader
        title="系统设置"
        breadcrumb={[{ label: '系统配置' }]}
        description="存储源、AI Provider、管理员密码与运维工具。"
      />

      <div className="mb-5">
        <StorageSourcesSection />
      </div>

      <div className="mb-5">
        <AiProvidersSection />
      </div>

      <Section title="运维" icon={<Wrench size={14} />}>
        <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
          <div className="card">
            <h2 className="mb-4 text-base font-bold text-txt">管理员密码</h2>
            <label className="mb-1 block text-xs text-muted">重置密码（留空则不修改）</label>
            <input type="password" className="input" value={adminPassword} onChange={(e) => setAdminPassword(e.target.value)} placeholder="新密码" />
            <button className="btn-primary mt-3" onClick={() => savePwdMut.mutate()} disabled={savePwdMut.isPending || !adminPassword}>
              {savePwdMut.isPending ? '保存中...' : '更新密码'}
            </button>
          </div>

          <div className="card">
            <h2 className="mb-2 text-base font-bold text-txt">视频时长探测</h2>
            <p className="mb-3 text-xs text-muted">使用 ffprobe 探测缺准时长的课时，串行限速约每集 4 秒。</p>
            <button className="btn-secondary inline-flex items-center gap-1.5" onClick={() => probeMut.mutate()} disabled={probeMut.isPending}>
              {probeMut.isPending ? '排队中...' : <><Radio size={14} /> 扫描缺失时长</>}
            </button>
          </div>
        </div>
      </Section>
    </div>
  );
}
