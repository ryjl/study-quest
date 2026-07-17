import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from './lib/api';
import { Layout } from './components/Layout';
import { Login } from './pages/Login';
import { Dashboard } from './pages/Dashboard';
import { Courses } from './pages/Courses';
import { Users } from './pages/Users';
import { Badges } from './pages/Badges';
import { Import } from './pages/Import';
import { Settings } from './pages/Settings';
import { Classification } from './pages/Classification';
import { Releases } from './pages/Releases';
import { ReadingRoom } from './pages/ReadingRoom';
import { WatchHistory } from './pages/WatchHistory';
import { SubtitleQueue } from './pages/SubtitleQueue';
import { AIWorkflow } from './pages/AIWorkflow';
import { AIUserView } from './pages/AIUserView';
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
        <Route path="ai-workflow" element={<AIWorkflow />} />
        <Route path="ai-user" element={<AIUserView />} />
        <Route path="reading-room" element={<ReadingRoom />} />
        <Route path="classification" element={<Classification />} />
        <Route path="import" element={<Import />} />
        <Route path="badges" element={<Badges />} />
        <Route path="releases" element={<Releases />} />
        <Route path="settings" element={<Settings />} />
      </Route>
      <Route path="*" element={<Navigate to="/admin" replace />} />
    </Routes>
  );
}
