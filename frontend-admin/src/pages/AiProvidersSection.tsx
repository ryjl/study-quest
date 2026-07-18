import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { RefreshCw, Plug } from 'lucide-react';
import { api } from '../lib/api';
import { useToast } from '../lib/toast';
import { useAiProviders, useInvalidateAiProviders } from '../lib/useAiProviders';
import type { AiProvider, AiModelsResult } from '../lib/types';

// AiProvidersSection is the AI-provider management card on the Settings page.
//
// Round-3 redesign: the embedding model is bundled in the docker image and
// auto-seeded on boot (ai.SeedLocalEmbedding), so it is NOT configurable here —
// the admin only configures the single chat provider. There is exactly one chat
// config (not a list), so this is a single fixed form:
//   - no chat row yet → "初次配置" (creates one);
//   - chat row exists → "编辑" (updates it).
// No add/delete — chat is a singleton config. base_url + api_key are entered,
// then "拉取可用模型" probes the relay's /v1/models so the operator picks the
// model from a dropdown instead of typing a possibly-wrong id.
export function AiProvidersSection() {
  const providersQ = useAiProviders();
  const invalidate = useInvalidateAiProviders();

  // The embedding row is auto-seeded; we only surface chat to the admin.
  const chatProvider = providersQ.data?.find((p) => p.capability === 'chat') ?? null;

  if (providersQ.isLoading) {
    return (
      <div className="card">
        <h2 className="text-base font-bold text-txt">AI Provider 配置</h2>
        <p className="mt-2 text-sm text-muted">加载中…</p>
      </div>
    );
  }

  return (
    <div className="card">
      <div className="mb-4">
        <h2 className="text-base font-bold text-txt">AI Provider 配置</h2>
        <p className="mt-0.5 text-xs text-muted">
          配置聊天模型(OpenAI 兼容端点)。向量模型已内置,无需配置。
        </p>
      </div>
      <ChatProviderForm provider={chatProvider} onSaved={invalidate} />
    </div>
  );
}

function ChatProviderForm({ provider, onSaved }: { provider: AiProvider | null; onSaved: () => void }) {
  const toast = useToast();
  const isEdit = !!provider;

  const [name, setName] = useState(provider?.name ?? '主聊天模型');
  const [baseUrl, setBaseUrl] = useState(provider?.base_url ?? '');
  // api_key is sensitive: the server never echoes it back on GET. In edit mode
  // leaving it blank = "don't change" (we omit the field on submit).
  const [apiKey, setApiKey] = useState('');
  const [modelName, setModelName] = useState(provider?.model_name ?? '');
  const [isEnabled, setIsEnabled] = useState(provider?.is_enabled ?? true);

  // Model dropdown state: fetched from the relay's /v1/models after base_url +
  // api_key are entered. Empty = not yet fetched (the input falls back to a
  // plain text field so a relay without /v1/models can still be configured).
  const [models, setModels] = useState<string[]>([]);
  const [modelsFetched, setModelsFetched] = useState(false);

  const fetchModelsMut = useMutation({
    // The models probe hits the relay directly with the entered key (the saved
    // key is never echoed back, so we can't reuse it here). In edit mode the
    // operator must re-enter the key to pull models — acceptable since this is
    // an occasional diagnostic action, not the save path.
    mutationFn: () => api.fetchAiModels(baseUrl.trim(), apiKey.trim()),
    onSuccess: (d: AiModelsResult) => {
      if (d.ok && d.models) {
        setModels(d.models);
        setModelsFetched(true);
        toast.success(`拉取到 ${d.models.length} 个可用模型`);
      } else {
        setModels([]);
        setModelsFetched(false);
        toast.error(d.message || '拉取模型失败');
      }
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const saveMut = useMutation({
    mutationFn: (body: AiProvider) =>
      isEdit && provider?.id ? api.updateAiProvider(provider.id, body) : api.createAiProvider(body),
    onSuccess: () => {
      toast.success(isEdit ? '已保存' : '已配置');
      onSaved();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  const submit = () => {
    if (!name.trim() || !baseUrl.trim() || !modelName.trim()) return;
    // On create, api_key is required (can't call the relay without it). On edit,
    // blank api_key = keep existing.
    if (!isEdit && !apiKey.trim()) return;
    const body: AiProvider = {
      capability: 'chat',
      name: name.trim(),
      provider_type: 'openai_compat',
      base_url: baseUrl.trim(),
      // Edit + blank key = don't change: send empty string (backend treats "" as
      // no-op on update). Mirrors the admin-password "留空则不修改" convention.
      api_key: apiKey,
      model_name: modelName.trim(),
      is_enabled: isEnabled,
    };
    saveMut.mutate(body);
  };

  const canFetchModels = baseUrl.trim() !== '' && apiKey.trim() !== '';
  const canSubmit = name.trim() !== '' && baseUrl.trim() !== '' && modelName.trim() !== '' && (isEdit || apiKey.trim() !== '');

  return (
    <div className="space-y-3">
      <div>
        <label className="mb-1 block text-xs text-muted">名称</label>
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="如：主聊天模型" />
      </div>
      <div>
        <label className="mb-1 block text-xs text-muted">Base URL</label>
        <input
          className="input font-mono"
          value={baseUrl}
          onChange={(e) => setBaseUrl(e.target.value)}
          placeholder="https://www.hi-code.cc"
        />
      </div>
      <div>
        <label className="mb-1 block text-xs text-muted">API Key{isEdit ? '（留空则不修改）' : ''}</label>
        <input
          type="password"
          className="input font-mono"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          placeholder={isEdit ? '••••••（不修改请留空）' : 'sk-...'}
        />
      </div>
      <div>
        <div className="mb-1 flex items-center justify-between">
          <label className="block text-xs text-muted">模型</label>
          <button
            type="button"
            className="btn-secondary btn-sm inline-flex items-center gap-1.5"
            onClick={() => fetchModelsMut.mutate()}
            disabled={!canFetchModels || fetchModelsMut.isPending}
          >
            {fetchModelsMut.isPending ? '拉取中…' : <><RefreshCw size={14} /> 拉取可用模型</>}
          </button>
        </div>
        {modelsFetched && models.length > 0 ? (
          <select className="input font-mono" value={modelName} onChange={(e) => setModelName(e.target.value)}>
            <option value="" disabled>
              选择模型…
            </option>
            {models.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        ) : (
          <input
            className="input font-mono"
            value={modelName}
            onChange={(e) => setModelName(e.target.value)}
            placeholder={modelsFetched && models.length === 0 ? '未拉取到模型,请手动填写' : 'gpt-5.4-mini（填好 URL+Key 后可点上方拉取）'}
          />
        )}
        {modelsFetched && models.length === 0 && (
          <p className="mt-1 text-[11px] text-muted">该端点未返回模型列表,可直接手动输入模型名。</p>
        )}
      </div>
      <div>
        <label className="flex h-[38px] items-center gap-2 text-sm text-txt">
          <input type="checkbox" checked={isEnabled} onChange={(e) => setIsEnabled(e.target.checked)} className="h-4 w-4 accent-primary" />
          <span className="text-xs text-muted">启用此 Provider</span>
        </label>
      </div>
      <div className="flex items-center justify-end gap-2 pt-1">
        {isEdit && <ConnectionTestButton id={provider!.id!} />}
        <button className="btn-primary" onClick={submit} disabled={saveMut.isPending || !canSubmit}>
          {saveMut.isPending ? '保存中…' : isEdit ? '保存' : '配置'}
        </button>
      </div>
    </div>
  );
}

// ConnectionTestButton reuses the existing per-row test endpoint. Kept as a
// small component so the form stays readable; after save the row may have a new
// id, but for the singleton chat config the id is stable within a session.
function ConnectionTestButton({ id }: { id: number }) {
  const toast = useToast();
  const testMut = useMutation({
    mutationFn: () => api.testAiProvider(id),
    onSuccess: (d) => {
      if (d.ok) toast.success(d.message || '连接成功');
      else toast.error(d.message || '连接失败');
    },
    onError: (e) => toast.error((e as Error).message),
  });
  return (
    <button className="btn-secondary inline-flex items-center gap-1.5" onClick={() => testMut.mutate()} disabled={testMut.isPending}>
      {testMut.isPending ? '测试中…' : <><Plug size={14} /> 测试连接</>}
    </button>
  );
}
