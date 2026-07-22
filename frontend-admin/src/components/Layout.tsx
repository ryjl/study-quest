import { useState, type ReactNode } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { pollWhileActive } from '../lib/query';
import { useSubjects } from '../lib/useSubjects';
import { useStorageSources } from '../lib/useStorageSources';
import { useTags } from '../lib/useTags';
import { useTheme } from './ThemeProvider';
import {
  LayoutGrid,
  Library,
  Captions,
  BookOpen,
  Users,
  Calendar,
  Bot,
  Tags,
  Medal,
  Package,
  Settings,
  Moon,
  Sun,
  LogOut,
  ChevronRight,
  Loader2,
  ScrollText,
} from 'lucide-react';

interface NavItem {
  to: string;
  label: string;
  icon: ReactNode;
  end?: boolean;
}

interface NavGroup {
  group: string;
  items: NavItem[];
}

// Task-oriented navigation grouped into 5 collapsible sections. The previous
// flat 14-item list read like a feature inventory; grouping by user task
// (operating content, managing users, AI ops, system config) gives the
// sidebar product-level structure. The 概览 group holds only the dashboard.
// Icons are lucide-react line icons (Linear/Notion pattern), sized 16px.
const NAV_GROUPS: NavGroup[] = [
  {
    group: '概览',
    items: [{ to: '/admin/', label: '控制台', icon: <LayoutGrid size={16} />, end: true }],
  },
  {
    group: '内容运营',
    items: [
      { to: '/admin/courses', label: '课程库管理', icon: <Library size={16} /> },
      { to: '/admin/subtitle-queue', label: '字幕队列', icon: <Captions size={16} /> },
      { to: '/admin/reading-room', label: '阅读室', icon: <BookOpen size={16} /> },
    ],
  },
  {
    group: '用户与授权',
    items: [
      { to: '/admin/users', label: '用户与授权', icon: <Users size={16} /> },
      { to: '/admin/watch-history', label: '观看历史', icon: <Calendar size={16} /> },
    ],
  },
  {
    group: 'AI 运营',
    items: [
      // 2026-07-19 集中化:原 AI Workflow + AI 用户视图 + CourseModal 里的 AI 配置 +
      // Settings 里的 Provider 全部并入这个 AI 控制台。各功能用 ?tab= 切换。
      { to: '/admin/ai-console', label: 'AI 控制台', icon: <Bot size={16} /> },
    ],
  },
  {
    group: '系统配置',
    items: [
      { to: '/admin/classification', label: '分类与标签', icon: <Tags size={16} /> },
      { to: '/admin/badges', label: '荣誉徽章', icon: <Medal size={16} /> },
      { to: '/admin/releases', label: '版本发布', icon: <Package size={16} /> },
      { to: '/admin/settings', label: '系统设置', icon: <Settings size={16} /> },
      { to: '/admin/logs', label: '系统日志', icon: <ScrollText size={16} /> },
    ],
  },
];

export function Layout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { theme, toggleTheme } = useTheme();
  // Tracks groups the user has explicitly collapsed. A group containing the
  // active route is force-expanded below (see effectiveCollapsed), so the
  // user never loses their current page when toggling other groups.
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());

  const toggleGroup = (group: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(group)) next.delete(group);
      else next.add(group);
      return next;
    });
  };

  // Warm the subject catalog cache for every page (subjects are referenced
  // across courses, dashboard, import, badges).
  useSubjects();
  useTags();
  useStorageSources();
  const probeQ = useQuery({
    queryKey: ['probe'],
    queryFn: api.probeProgress,
    refetchInterval: pollWhileActive(2000),
    refetchIntervalInBackground: false,
  });

  const running = probeQ.data?.running;
  const progress = probeQ.data?.total ? Math.round((probeQ.data.done / probeQ.data.total) * 100) : 0;

  const onLogout = async () => {
    try {
      await api.logout();
    } catch {
      /* ignore */
    }
    navigate('/admin/login');
  };

  return (
    <div className="flex min-h-screen">
      {/* Sidebar — 240px (w-60), tighter than the old 256px. */}
      <aside className="fixed left-0 top-0 z-30 flex h-screen w-60 flex-col border-r border-border bg-card px-3 py-4">
        {/* Logo: small mark + wordmark. No gradient text — just font weight. */}
        <div className="mb-6 flex items-center gap-2 px-2">
          <div className="flex h-7 w-7 items-center justify-center rounded-md bg-primary text-xs font-bold text-bg">S</div>
          <span className="text-base font-semibold tracking-tight text-txt">StudyQuest</span>
        </div>

        <nav className="flex flex-1 flex-col gap-0.5 overflow-y-auto">
          {NAV_GROUPS.map((g, gi) => {
            // Auto-expand on active: if any item matches the current path the
            // group is always shown, regardless of the user's toggle state.
            const groupIsActive = g.items.some((it) =>
              it.end ? location.pathname === it.to : location.pathname.startsWith(it.to),
            );
            const isCollapsed = !groupIsActive && collapsed.has(g.group);
            return (
              <div key={g.group}>
                <button
                  type="button"
                  onClick={() => toggleGroup(g.group)}
                  className={`flex w-full items-center gap-1.5 rounded-md px-2 text-[11px] font-medium uppercase tracking-wider text-muted transition-colors hover:bg-card-2 hover:text-txt ${gi === 0 ? 'mt-0' : 'mt-4'}`}
                >
                  <ChevronRight size={11} className={`transition-transform duration-150 ${isCollapsed ? '' : 'rotate-90'}`} />
                  <span>{g.group}</span>
                </button>
                {!isCollapsed && (
                  <div className="mt-0.5 flex flex-col gap-0.5">
                    {g.items.map((n) => (
                      <NavLink
                        key={n.to}
                        to={n.to}
                        end={n.end}
                        className={({ isActive }) =>
                          // Active: muted bg + left accent bar (via border-l) +
                          // primary-colored text. No gradient fill, no glow.
                          isActive
                            ? 'relative flex items-center gap-2.5 rounded-md bg-card-2 px-2.5 py-1.5 text-sm font-medium text-txt before:absolute before:left-0 before:top-1/2 before:h-4 before:w-0.5 before:-translate-y-1/2 before:rounded-full before:bg-primary'
                            : 'flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm text-muted transition-colors hover:bg-card-2 hover:text-txt'
                        }
                      >
                        <span className="flex-shrink-0">{n.icon}</span>
                        {n.label}
                      </NavLink>
                    ))}
                  </div>
                )}
              </div>
            );
          })}
        </nav>

        {/* Probe progress indicator */}
        {running && (
          <div className="mb-2 mt-3 rounded-lg border border-border bg-card-2/50 p-2.5">
            <div className="mb-1.5 flex items-center justify-between text-xs">
              <span className="flex items-center gap-1.5 text-txt">
                <Loader2 size={12} className="animate-spin text-muted" />
                探测时长中
              </span>
              <span className="tabular-nums text-muted">
                {probeQ.data?.done}/{probeQ.data?.total}
              </span>
            </div>
            <div className="h-1 overflow-hidden rounded-full bg-card">
              <div className="h-full rounded-full bg-txt transition-all" style={{ width: `${progress}%` }} />
            </div>
            {probeQ.data?.current_title && <div className="mt-1 truncate text-[10px] text-muted">{probeQ.data.current_title}</div>}
          </div>
        )}

        <button
          onClick={toggleTheme}
          className="mb-1 flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm text-muted transition-colors hover:bg-card-2 hover:text-txt"
          title={theme === 'light' ? '切换到暗色主题' : '切换到亮色主题'}
        >
          {theme === 'light' ? <Moon size={16} /> : <Sun size={16} />}
          {theme === 'light' ? '暗色主题' : '亮色主题'}
        </button>
        <button
          onClick={onLogout}
          className="flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm text-muted transition-colors hover:bg-card-2 hover:text-bad"
        >
          <LogOut size={16} />
          退出登录
        </button>
      </aside>

      {/* Main content — px-8 py-6 (was p-10). PageHeader uses -mx-8 to span full width. */}
      <main className="ml-60 flex-1 px-8 py-6">
        <Outlet />
      </main>
    </div>
  );
}
