import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { useSubjects } from '../lib/useSubjects';
import { useTags } from '../lib/useTags';
import { useTheme } from './ThemeProvider';

const NAV = [
  { to: '/admin/', label: '控制台', icon: '📊', end: true },
  { to: '/admin/users', label: '用户与授权', icon: '👥' },
  { to: '/admin/watch-history', label: '观看历史', icon: '📅' },
  { to: '/admin/courses', label: '课程库管理', icon: '📚' },
  { to: '/admin/reading-room', label: '阅读室', icon: '📖' },
  { to: '/admin/subjects', label: '科目管理', icon: '🏷️' },
  { to: '/admin/tags', label: '标签管理', icon: '🔖' },
  { to: '/admin/import', label: '文件导入', icon: '📥' },
  { to: '/admin/badges', label: '荣誉徽章', icon: '🏅' },
  { to: '/admin/releases', label: '版本发布', icon: '📦' },
  { to: '/admin/settings', label: '系统设置', icon: '⚙️' },
];

export function Layout() {
  const navigate = useNavigate();
  const { theme, toggleTheme } = useTheme();
  // Warm the subject catalog cache for every page (subjects are referenced
  // across courses, dashboard, import, badges).
  useSubjects();
  useTags();
  const probeQ = useQuery({
    queryKey: ['probe'],
    queryFn: api.probeProgress,
    refetchInterval: (q) => (q.state.data?.running ? 2000 : false),
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
      {/* Sidebar */}
      <aside className="fixed left-0 top-0 z-30 flex h-screen w-64 flex-col border-r border-border bg-card p-6">
        <div className="mb-10 bg-gradient-to-r from-primary to-primary-dark bg-clip-text text-2xl font-bold text-transparent">
          StudyQuest
        </div>
        <nav className="flex flex-1 flex-col gap-1.5">
          {NAV.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.end}
              className={({ isActive }) =>
                `flex items-center gap-3 rounded-xl px-4 py-3 text-sm font-medium transition-all ${
                  isActive ? 'bg-gradient-to-br from-primary to-primary-dark text-white shadow-primary-glow' : 'text-muted hover:bg-card-2 hover:text-txt'
                }`
              }
            >
              <span className="text-base">{n.icon}</span>
              {n.label}
            </NavLink>
          ))}
        </nav>

        {/* Probe progress indicator */}
        {running && (
          <div className="mb-3 rounded-xl border border-primary/30 bg-primary/10 p-3">
            <div className="mb-1.5 flex items-center justify-between text-xs">
              <span className="text-primary">⏱ 探测时长中</span>
              <span className="text-muted">
                {probeQ.data?.done}/{probeQ.data?.total}
              </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-card-2">
              <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${progress}%` }} />
            </div>
            {probeQ.data?.current_title && <div className="mt-1 truncate text-[10px] text-muted">{probeQ.data.current_title}</div>}
          </div>
        )}

        <button
          onClick={toggleTheme}
          className="mb-2 flex items-center gap-2 rounded-xl px-4 py-2.5 text-sm font-medium text-muted transition hover:bg-card-2 hover:text-txt"
          title={theme === 'light' ? '切换到暗色主题' : '切换到亮色主题'}
        >
          <span className="text-base">{theme === 'light' ? '🌙' : '☀️'}</span>
          {theme === 'light' ? '暗色主题' : '亮色主题'}
        </button>
        <button onClick={onLogout} className="flex items-center gap-2 rounded-xl px-4 py-3 text-sm font-medium text-bad transition hover:bg-bad/10">
          ⏏ 退出登录
        </button>
      </aside>

      {/* Main content */}
      <main className="ml-64 flex-1 p-10">
        <Outlet />
      </main>
    </div>
  );
}
