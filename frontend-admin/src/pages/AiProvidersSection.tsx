import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api } from '../lib/api';
import { Modal } from '../components/ui';
import { useToast } from '../lib/toast';
import { useDeleteConfirm } from '../lib/useDeleteConfirm';
import { useAiProviders, useInvalidateAiProviders } from '../lib/useAiProviders';
import type { AiProvider, AiProviderTestResult } from '../lib/types';

// AiProvidersSection is the AI-provider management card on the Settings page.
// Renders the list of configured providers with edit/delete/test-connection
// actions, plus an "add provider" button that opens a modal form. Structure
// mirrors StorageSourcesSection 1:1.
//
// A provider row carries: name, capability (chat/embedding/rerank),
// provider_type (openai_compat/onnx_local), base_url, api_key (sensitive),
// model_name, is_enabled. The modal form is shared between create and edit.
export function AiProvidersSection() {
  const providersQ = useAiProviders();
  const invalidate = useInvalidateAiProviders();
  const toast = useToast();
  const [editing, setEditing] = useState<AiProvider | null>(null);
  const [open, setOpen] = useState(false);

  const del = useDeleteConfirm({ mutationFn: api.deleteAiProvider, noun: 'AI Provider', onDeleted: invalidate });

  const createMut = useMutation({
    mutationFn: (body: AiProvider) => api.createAiProvider(body),
    onSuccess: () => { toast.success('已创建'); invalidate(); setOpen(false); },
    onError: (e) => toast.error((e as Error).message),
  });
  const updateMut = useMutation({
    mutationFn: ({ id, body }: { id: number; body: AiProvider }) => api.updateAiProvider(id, body),
    onSuccess: () => { toast.success('已保存'); invalidate(); setOpen(false); },
    onError: (e) => toast.error((e as Error).message),
  });

  const openCreate = () => { setEditing(null); setOpen(true); };
  const openEdit = (p: AiProvider) => { setEditing(p); setOpen(true); };

  return (
    <div className="card">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h2 className="text-base font-bold text-txt">AI Provider 配置</h2>
          <p className="mt-0.5 text-xs text-muted">配置聊天 / 向量 / 重排模型。openai_compat 走 HTTP，onnx_local 走本地推理。</p>
        </div>
        <button className="btn-primary btn-sm" onClick={openCreate}>+ 新增 Provider</button>
      </div>

      {providersQ.isLoading ? (
        <p className="text-sm text-muted">加载中…</p>
      ) : providersQ.data && providersQ.data.length > 0 ? (
        <div className="space-y-2">
          {providersQ.data.map((p) => (
            <ProviderRow key={p.id} provider={p} onEdit={() => openEdit(p)} onDelete={() => del.confirmAndDelete(p.id!, `确认删除 AI Provider「${p.name}」？`)} />
          ))}
        </div>
      ) : (
        <p className="text-sm text-muted">尚未配置 AI Provider。新增后相关能力即可调用。</p>
      )}

      {open && (
        <ProviderModal
          provider={editing}
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

function ProviderRow({ provider, onEdit, onDelete }: { provider: AiProvider; onEdit: () => void; onDelete: () => void }) {
  const toast = useToast();
  const [result, setResult] = useState<AiProviderTestResult | null>(null);
  const testMut = useMutation({
    mutationFn: () => api.testAiProvider(provider.id!),
    onSuccess: (d) => {
      setResult(d);
      if (d.ok) toast.success(d.message || '连接成功');
      else toast.error(d.message || '连接失败');
    },
    onError: (e) => {
      setResult(null);
      toast.error((e as Error).message);
    },
  });
  return (
    <div className="rounded-lg border border-border bg-card-2 px-3 py-2">
      <div className="flex items-center gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold text-txt">{provider.name}</span>
            {!provider.is_enabled && <span className="rounded bg-border px-1.5 py-0.5 text-[10px] text-muted">已停用</span>}
            <span className="rounded bg-primary/15 px-1.5 py-0.5 text-[10px] text-primary uppercase">{provider.capability}</span>
            <span className="rounded bg-border px-1.5 py-0.5 text-[10px] text-muted font-mono">{provider.provider_type}</span>
          </div>
          <div className="truncate text-xs text-muted font-mono">
            {provider.base_url ? `${provider.base_url} · ` : ''}{provider.model_name}
          </div>
        </div>
        <button className="btn-secondary btn-sm" onClick={() => testMut.mutate()} disabled={testMut.isPending}>
          {testMut.isPending ? '测试中…' : '🔌 测试'}
        </button>
        <button className="btn-ghost btn-sm" onClick={onEdit}>编辑</button>
        <button className="btn-danger btn-sm" onClick={onDelete}>删除</button>
      </div>
      {result && (
        <div className={`mt-2 text-xs ${result.ok ? 'text-primary' : 'text-muted'}`}>
          {result.ok ? '✓' : '✗'} {result.message}{typeof result.latency_ms === 'number' ? ` · ${result.latency_ms}ms` : ''}
        </div>
      )}
    </div>
  );
}

function ProviderModal({ provider, pending, onCancel, onSubmit }: {
  provider: AiProvider | null;
  pending: boolean;
  onCancel: () => void;
  onSubmit: (body: AiProvider) => void;
}) {
  const isEdit = !!provider;
  const [name, setName] = useState(provider?.name ?? '');
  const [capability, setCapability] = useState<AiProvider['capability']>(provider?.capability ?? 'chat');
  const [providerType, setProviderType] = useState(provider?.provider_type ?? 'openai_compat');
  const [baseUrl, setBaseUrl] = useState(provider?.base_url ?? '');
  // api_key is sensitive: the server does NOT echo it back on GET, so we never
  // pre-fill it. In edit mode, leaving it blank = "don't change" (we omit the
  // field). Mirrors Settings.tsx admin-password "留空则不修改" convention.
  const [apiKey, setApiKey] = useState('');
  const [modelName, setModelName] = useState(provider?.model_name ?? '');
  const [isEnabled, setIsEnabled] = useState(provider?.is_enabled ?? true);

  const isOpenAi = providerType === 'openai_compat';

  const submit = () => {
    if (!name.trim() || !modelName.trim()) return;
    const body: AiProvider = {
      capability,
      name: name.trim(),
      provider_type: providerType,
      base_url: isOpenAi ? baseUrl.trim() : '',
      // Edit mode + blank key = don't change: send empty string and let the
      // backend decide. (Backend treats "" as no-op on update.)
      api_key: !isOpenAi ? '' : apiKey,
      model_name: modelName.trim(),
      is_enabled: isEnabled,
    };
    onSubmit(body);
  };

  return (
    <Modal open={true} onClose={onCancel} title={isEdit ? '编辑 AI Provider' : '新增 AI Provider'} size="md">
      <div className="space-y-3">
        <div>
          <label className="mb-1 block text-xs text-muted">名称</label>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="如：主聊天模型" />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="mb-1 block text-xs text-muted">能力</label>
            <select className="input" value={capability} onChange={(e) => setCapability(e.target.value as AiProvider['capability'])}>
              <option value="chat">chat（对话）</option>
              <option value="embedding">embedding（向量）</option>
              <option value="rerank">rerank（重排）</option>
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">Provider 类型</label>
            <select className="input" value={providerType} onChange={(e) => setProviderType(e.target.value)}>
              <option value="openai_compat">openai_compat</option>
              <option value="onnx_local">onnx_local</option>
            </select>
          </div>
        </div>
        {isOpenAi && (
          <div>
            <label className="mb-1 block text-xs text-muted">Base URL</label>
            <input className="input font-mono" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="https://www.hi-code.cc" />
          </div>
        )}
        {isOpenAi && (
          <div>
            <label className="mb-1 block text-xs text-muted">
              API Key{isEdit ? '（留空则不修改）' : ''}
            </label>
            <input type="password" className="input font-mono" value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder={isEdit ? '••••••（不修改请留空）' : 'sk-...'} />
          </div>
        )}
        <div>
          <label className="mb-1 block text-xs text-muted">模型名 / 模型路径</label>
          <input className="input font-mono" value={modelName} onChange={(e) => setModelName(e.target.value)} placeholder={isOpenAi ? 'gpt-5.4-mini' : '/models/bge-small.onnx'} />
        </div>
        <div>
          <label className="flex h-[38px] items-center gap-2 text-sm text-txt">
            <input type="checkbox" checked={isEnabled} onChange={(e) => setIsEnabled(e.target.checked)} className="h-4 w-4 accent-primary" />
            <span className="text-xs text-muted">启用此 Provider</span>
          </label>
        </div>
        <div className="flex justify-end gap-2 pt-2">
          <button className="btn-secondary" onClick={onCancel}>取消</button>
          <button className="btn-primary" onClick={submit} disabled={pending || !name.trim() || !modelName.trim()}>
            {pending ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </Modal>
  );
}
