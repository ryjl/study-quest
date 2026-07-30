import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Wrench, Radio, Gauge } from 'lucide-react';
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
  const [polishConcurrency, setPolishConcurrency] = useState('');

  // 读当前 polish_concurrency(回显到输入框)。密码是单向的(不回显),并发需要回显,
  // 所以单独 query。
  const settingsQuery = useQuery({
    queryKey: ['settings'],
    queryFn: api.getSettings,
  });

  // 并发值到了再同步到本地 input(只同步一次,避免用户输入时被覆盖)。
  const currentConc = settingsQuery.data?.polish_concurrency ?? '1';
  if (polishConcurrency === '' && currentConc) {
    setPolishConcurrency(currentConc);
  }

  const savePwdMut = useMutation({
    mutationFn: () => api.updateSettings({ admin_password: adminPassword }),
    onSuccess: (d) => {
      toast.success(d.message || '密码已更新');
      setAdminPassword('');
      qc.invalidateQueries({ queryKey: ['settings'] });
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const saveConcMut = useMutation({
    mutationFn: () => api.updateSettings({ polish_concurrency: Number(polishConcurrency) }),
    onSuccess: (d) => {
      toast.success(d.message || '已更新');
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

      {/* AI Provider 归位到系统设置:它是"一次性配置"(配好就忘),不属于高频 AI 运营,
          所以从 AI 控制台挪到这里,和存储源并列。控制台专注产出/调优/观测。 */}
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
            <h2 className="mb-1 inline-flex items-center gap-1.5 text-base font-bold text-txt">
              <Gauge size={15} /> AI 性能
            </h2>
            <label className="mb-1 block text-xs text-muted">字幕润色并发数（1~10，默认 3）</label>
            <input
              type="number"
              min={1}
              max={10}
              className="input"
              value={polishConcurrency}
              onChange={(e) => setPolishConcurrency(e.target.value)}
              placeholder="3"
            />
            <p className="mt-2 text-xs text-muted">
              润色时同时进行的 LLM 请求数（默认 3）。实测家里用的中继并发 3 稳定、并发 4 偶发限流；如遇 429 失败可调低到 2，换了不限制并发的中继可调高。
            </p>
            <button
              className="btn-primary mt-3"
              onClick={() => saveConcMut.mutate()}
              disabled={saveConcMut.isPending || polishConcurrency === '' || polishConcurrency === currentConc}
            >
              {saveConcMut.isPending ? '保存中...' : '保存'}
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
