import { useSearchParams } from 'react-router-dom';
import { PageHeader } from '../components/PageHeader';
import { AIWorkflow } from './AIWorkflow';
import { AIUserView } from './AIUserView';
import { AiProvidersSection } from './AiProvidersSection';
import { RegenTab } from './ai-console/RegenTab';
import { PromptConfigTab } from './ai-console/PromptConfigTab';

// AIConsole — the central AI operations page. 5 button tabs:
//   - regen (default): regen + delete summaries / advice / quizzes / reports
//   - prompt: subject-default + course-override prompt editor
//   - jobs: existing AIWorkflow (task queue + decision-trace replay)
//   - users: existing AIUserView (per-student observability)
//   - providers: existing AiProvidersSection (chat backend config)
//
// Tab state lives in the URL (?tab=...) so a refresh or shared link lands on
// the same tab; setParams uses replace:true so we don't pollute history as
// the admin clicks through tabs.
//
// AiProvidersSection is wrapped in a card so its visual weight matches the
// other tabs (it was originally a Settings card, hence the wrapper).

const TABS = [
  // 这个 tab 既有重新生成也有删除,叫"内容管理"比"重新生成"更准。
  { key: 'regen', label: '内容管理' },
  { key: 'prompt', label: 'Prompt 配置' },
  { key: 'jobs', label: '任务队列' },
  { key: 'users', label: '学生数据' },
  { key: 'providers', label: 'Provider' },
] as const;

type TabKey = (typeof TABS)[number]['key'];

function isTabKey(s: string | null): s is TabKey {
  return !!s && (TABS as readonly { key: string }[]).some((t) => t.key === s);
}

export function AIConsole() {
  const [params, setParams] = useSearchParams();
  const raw = params.get('tab');
  const tab: TabKey = isTabKey(raw) ? raw : 'regen';
  const setTab = (t: string) => {
    const next = new URLSearchParams(params);
    next.set('tab', t);
    setParams(next, { replace: true });
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="AI 控制台"
        breadcrumb={[{ label: 'AI 运营' }]}
        description="集中管理 AI 内容生命周期:重新生成、删除、调 Prompt、观测队列与学生数据。"
      />

      <div className="flex flex-wrap gap-1.5 border-b border-border">
        {TABS.map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`rounded-t-md px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key ? 'border-b-2 border-primary text-primary' : 'text-muted hover:text-txt'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div>
        {tab === 'regen' && <RegenTab />}
        {tab === 'prompt' && <PromptConfigTab />}
        {/* embedded=true 让 AIWorkflow/AIUserView 跳过自己的 PageHeader,
            由 AIConsole 顶部统一提供"AI 控制台"标题,避免双 header。 */}
        {tab === 'jobs' && <AIWorkflow embedded />}
        {tab === 'users' && <AIUserView embedded />}
        {tab === 'providers' && (
          <div className="rounded-lg border border-border bg-card p-4">
            <AiProvidersSection />
          </div>
        )}
      </div>
    </div>
  );
}
