import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { RefreshCw, Plug, FlaskConical } from 'lucide-react';
import { api } from '../lib/api';
import { useToast } from '../lib/toast';
import { useAiProviders, useInvalidateAiProviders } from '../lib/useAiProviders';
import type { AiProvider, AiModelsResult, AiRealTestResult } from '../lib/types';

// AiProvidersSection — the single chat LLM config form.
//
// 项目当前是"单 LLM"架构:所有 AI 调用(润色/总结/出题/建议/报告/作业)走同一个
// chat provider。后端 resolver 的 purpose 分派机制(opt-in,没配 tag 就兜底用唯一
// chat provider)保留着留扩展余地,但前端不再暴露多 provider / 按 purpose 分工的 UI——
// 那会误导用户以为能配多个 provider,实际上 UI 早就只支持单个。
//
// embedding 模型(onnx 本地 BGE)内置在镜像里、不存 DB、不可配,所以这里只管 chat。
// 单例配置:没有 chat 行 → "初次配置"(创建);有 → "编辑"(更新)。没有增删多行。
// base_url + api_key 填好后,"拉取可用模型"探测中转站 /v1/models,operator 从下拉选模型,
// 而不是手敲一个可能写错的 id。
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
          配置唯一的聊天 LLM(OpenAI 兼容端点)——所有 AI 任务(润色/总结/出题/建议/报告/作业)共用它。向量模型已内置,无需配置。
        </p>
      </div>
      {/* key 钉在 provider.id 上:当外层 chatProvider 引用变化(保存后 invalidate
          refetch、或从无到有创建出第一行),强制整个表单 unmount/remount,useState
          重新用新的 provider.* 初始化。这是根治"切换/保存后 model_name 残留旧值"
          stale state bug 的关键——否则 useState 的 initialValue 只在首次 mount 生效,
          后续 provider prop 变化时 state 不会跟着重置。 */}
      <ChatProviderForm key={chatProvider?.id ?? 'new'} provider={chatProvider} onSaved={invalidate} />
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
    // edit 模式 key 框空(=不修改)时,传 providerId 让后端用 DB 已存 key——不再要求
    // admin 反复重输 key 才能拉模型列表。新建模式无 providerId,仍需手输 key。
    mutationFn: () => api.fetchAiModels(baseUrl.trim(), apiKey.trim(), isEdit && !apiKey.trim() ? provider?.id : undefined),
    onSuccess: (d: AiModelsResult) => {
      if (d.ok && d.models) {
        setModels(d.models);
        setModelsFetched(true);
        // 根治 stale model bug 的第二招:换中转站重新拉取后,旧 modelName 几乎一定
        // 不在新列表里(否则就没必要换了)。受控 <select> 在 value 不匹配任何 option
        // 时会静默显示空白,但 state 仍保留旧值——用户以为没选,保存却提交了过期 id,
        // 配上新 base_url 就 404。这里主动清空,让用户从新列表里重新选;若旧值恰好
        // 还在新列表里则保留(免得给用户增加无谓的重复操作)。
        if (!d.models.includes(modelName)) {
          setModelName('');
        }
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
      // 单 LLM 架构:不暴露 purpose 分工,tags 永远发空数组(后端 resolver 兜底走这个
      // provider)。tags 字段保留在类型里是为了后端兼容,这里只是 UI 不再让用户配。
      tags: [],
      is_enabled: isEnabled,
    };
    saveMut.mutate(body);
  };

  // edit 模式有 providerId 时,即使 key 框空(=不修改)也允许拉取——后端用 DB 已存 key。
  // 新建模式必须手输 key(没有已存的可用)。
  const canFetchModels = baseUrl.trim() !== '' && (apiKey.trim() !== '' || (isEdit && provider?.id != null));
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
      {/* 实战测试:发一个 quiz 规模的长输出请求,验证中转站能否扛住真实业务负载(连通性
          测试 max_tokens=5 测不出长输出超时 502 这类故障)。独立面板放在表单底部,因为
          结果信息量大(后端模型推测、响应头、输出采样),需要展开空间。不依赖已保存的
          provider——填了 URL+Key+Model 即可测,这样配新中转站选型时不用先保存。 */}
      <RealTestPanel baseUrl={baseUrl} apiKey={apiKey} modelName={modelName} providerId={isEdit ? provider?.id : undefined} />
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

// RealTestPanel 是 admin 的"实战测试"入口:发一个 quiz 规模的长输出请求(max_tokens=6000),
// 验证中转站能否扛住真实业务负载。连通性测试(max_tokens=5)测不出长输出超时 502 这类
// 故障——这正是本功能要暴露的。结果信息量大(后端模型推测、响应头、输出采样),用可展开
// 卡片展示:顶部一行成功/失败 + 耗时,主行显示后端模型推测,细节(响应头/采样/tokens)
// 放折叠区。不依赖已保存的 provider,填了 URL+Key+Model 即可测。
//
// 用 apiKey 作为入参:edit 模式下表单的 apiKey 可能是空(留空=不修改),此时按钮禁用——
// 和"拉取可用模型"按钮一致的策略,避免用空 key 发请求拿到误导性的 401。
function RealTestPanel({ baseUrl, apiKey, modelName, providerId }: { baseUrl: string; apiKey: string; modelName: string; providerId?: number }) {
  const [result, setResult] = useState<AiRealTestResult | null>(null);

  const testMut = useMutation({
    // providerId 提供 + key 空时,后端用 DB 已存 key(edit 模式复用,不用反复重输 key)。
    mutationFn: () => api.realTestAiProvider(baseUrl.trim(), apiKey.trim(), modelName.trim(), providerId),
    onSuccess: (d) => {
      setResult(d);
    },
    onError: (e) => {
      // 网络/路由层错误(request() 在非 2xx 时 throw)——包装成统一的失败结果渲染。
      setResult({ ok: false, message: (e as Error).message });
    },
  });

  // providerId 有值(edit 模式)时,即使 key 框空也允许测试——后端用 DB 已存 key。
  // 无 providerId(新建/未保存)时,必须手输 key。
  const canTest = baseUrl.trim() !== '' && modelName.trim() !== '' && (apiKey.trim() !== '' || providerId != null);

  return (
    <div className="mt-2 rounded-lg border border-border/60 bg-bg-secondary/40 p-3">
      <div className="flex items-center justify-between gap-2">
        <div>
          <div className="flex items-center gap-1.5 text-sm font-medium text-txt">
            <FlaskConical size={14} /> 实战测试
          </div>
          <p className="mt-0.5 text-[11px] text-muted">
            发一个 quiz 规模的长输出请求(模拟出题,max_tokens=6000),验证中转站能否扛住真实业务负载。{canTest ? '' : '(填好 URL + Key + 模型后可用)'}
          </p>
        </div>
        <button
          type="button"
          className="btn-secondary btn-sm inline-flex shrink-0 items-center gap-1.5"
          onClick={() => testMut.mutate()}
          disabled={!canTest || testMut.isPending}
        >
          {testMut.isPending ? '测试中…(最长 90 秒)' : <><FlaskConical size={14} /> 开始测试</>}
        </button>
      </div>

      {result && <RealTestResultCard result={result} />}
    </div>
  );
}

// RealTestResultCard 渲染实战测试结果。顶部大字状态行(成功绿/失败红)+ 耗时;主行显示
// 后端模型推测和诊断(失败时);细节(响应头/输出采样/tokens)放 <details> 折叠区,默认
// 收起——这些是给需要深入排查的 admin 看的,日常用主行状态判断就够了。
function RealTestResultCard({ result }: { result: AiRealTestResult }) {
  const ok = result.ok;
  return (
    <div className={`mt-3 rounded-md border p-3 text-sm ${ok ? 'border-green-500/40 bg-green-50/40 dark:bg-green-950/20' : 'border-red-500/40 bg-red-50/40 dark:bg-red-950/20'}`}>
      {/* 状态行:✅/❌ + 一句话结论 + 耗时 */}
      <div className="flex items-center justify-between gap-2">
        <span className={`font-semibold ${ok ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'}`}>
          {ok ? '✅ 实战测试通过' : '❌ 实战测试失败'}
        </span>
        {typeof result.latency_ms === 'number' && (
          <span className="font-mono text-xs text-muted">{(result.latency_ms / 1000).toFixed(1)} 秒</span>
        )}
      </div>

      {/* 后端模型推测(成功才有)——这是本功能的核心增值信息 */}
      {ok && result.real_model_hint && (
        <div className="mt-2 text-xs">
          <span className="text-muted">中转站后端:</span>{' '}
          <span className="font-medium text-txt">{result.real_model_hint}</span>
        </div>
      )}

      {/* 人话诊断(失败时才有)——帮 admin 快速定位是 502/超时/鉴权 */}
      {!ok && result.diagnosis && (
        <div className="mt-2 text-xs text-txt">{result.diagnosis}</div>
      )}

      {/* finish_reason != stop 高亮(成功但有容量/截断信号) */}
      {ok && result.finish_reason && result.finish_reason !== 'stop' && (
        <div className="mt-1 text-[11px] text-amber-600 dark:text-amber-400">
          ⚠️ finish_reason={result.finish_reason}(输出未正常结束;length=被 max_tokens 截断)
        </div>
      )}

      {/* 失败时也展示原始 message,让 admin 看到原始错误串 */}
      {!ok && (
        <div className="mt-1 font-mono text-[11px] text-muted">{result.message}</div>
      )}

      {/* 细节折叠区:请求规模、tokens、响应头、输出采样 */}
      {(result.request || result.usage || result.response_headers || result.sample_output) && (
        <details className="mt-2">
          <summary className="cursor-pointer text-[11px] text-muted hover:text-txt">详细信息</summary>
          <div className="mt-2 space-y-2">
            {result.request && (
              <div className="text-[11px] text-muted">
                请求规模:system prompt {result.request.system_prompt_chars} 字 + user prompt {result.request.user_prompt_chars} 字 · max_tokens={result.request.max_tokens} · temperature={result.request.temperature}
              </div>
            )}
            {result.usage && (
              <div className="text-[11px] text-muted">
                Token 消耗:prompt {result.usage.prompt_tokens} + completion {result.usage.completion_tokens} = {result.usage.total_tokens}
              </div>
            )}
            {result.response_headers && Object.keys(result.response_headers).length > 0 && (
              <div>
                <div className="mb-0.5 text-[11px] text-muted">响应头:</div>
                <pre className="overflow-x-auto rounded bg-bg/60 p-2 font-mono text-[10px] text-txt">
{Object.entries(result.response_headers).map(([k, v]) => `${k}: ${v}`).join('\n')}
                </pre>
              </div>
            )}
            {result.sample_output && (
              <div>
                <div className="mb-0.5 text-[11px] text-muted">模型输出采样(前 500 字):</div>
                <pre className="max-h-40 overflow-auto rounded bg-bg/60 p-2 font-mono text-[10px] text-txt">{result.sample_output}</pre>
              </div>
            )}
          </div>
        </details>
      )}
    </div>
  );
}
