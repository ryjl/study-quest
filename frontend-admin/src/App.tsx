import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from './lib/api';
import { Layout } from './components/Layout';
import { Login } from './pages/Login';
import { Dashboard } from './pages/Dashboard';
import { Courses } from './pages/Courses';
import { Users } from './pages/users/Users';
import { Badges } from './pages/Badges';
import { Settings } from './pages/Settings';
import { Classification } from './pages/Classification';
import { Releases } from './pages/Releases';
import { ReadingRoom } from './pages/reading-room/ReadingRoom';
import { WatchHistory } from './pages/WatchHistory';
import { SubtitleQueue } from './pages/SubtitleQueue';
import { AIConsole } from './pages/AIConsole';
import { Logs } from './pages/Logs';
import { Spinner } from './components/ui';

function AuthGuard({ children }: { children: React.ReactNode }) {
  const location = useLocation();
  const meQ = useQuery({ queryKey: ['me'], queryFn: api.me });

  if (meQ.isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Spinner size={32} />
      </div>
    );
  }
  if (!meQ.data?.authenticated) {
    return <Navigate to="/admin/login" replace state={{ from: location }} />;
  }
  return <>{children}</>;
}

export default function App() {
  return (
    <Routes>
      <Route path="/admin/login" element={<Login />} />
      <Route
        path="/admin"
        element={
          <AuthGuard>
            <Layout />
          </AuthGuard>
        }
      >
        <Route index element={<Dashboard />} />
        <Route path="users" element={<Users />} />
        <Route path="watch-history" element={<WatchHistory />} />
        <Route path="courses" element={<Courses />} />
        <Route path="subtitle-queue" element={<SubtitleQueue />} />
        {/* 2026-07-19 集中化:旧 AI Workflow / AI 用户视图路由重定向到 AI 控制台
            对应 tab,兼容老书签。AIConsole 内嵌原 AIWorkflow / AIUserView 组件,功能不丢。 */}
        <Route path="ai-workflow" element={<Navigate to="/admin/ai-console?tab=jobs" replace />} />
        <Route path="ai-user" element={<Navigate to="/admin/ai-console?tab=users" replace />} />
        <Route path="ai-console" element={<AIConsole />} />
        <Route path="reading-room" element={<ReadingRoom />} />
        <Route path="classification" element={<Classification />} />
        <Route path="badges" element={<Badges />} />
        <Route path="releases" element={<Releases />} />
        <Route path="settings" element={<Settings />} />
        <Route path="logs" element={<Logs />} />
      </Route>
      <Route path="*" element={<Navigate to="/admin" replace />} />
    </Routes>
  );
}
