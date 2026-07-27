import { Navigate, Route, Routes, useLocation, useSearchParams } from 'react-router-dom';
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
import { AIOverview } from './pages/ai/AIOverview';
import { CourseWorkbenchList } from './pages/ai/CourseWorkbenchList';
import { CourseWorkbench } from './pages/ai/CourseWorkbench';
import { StudentWorkbenchList } from './pages/ai/StudentWorkbenchList';
import { StudentWorkbench } from './pages/ai/StudentWorkbench';
import { AIWorkflow } from './pages/AIWorkflow';
import { Logs } from './pages/Logs';
import { Spinner } from './components/ui';

// LegacyAIConsoleRedirect — 把旧 /admin/ai-console?tab=xxx 的书签映射到新路径。
// 旧 AIConsole 6 个 tab 拆进了对象工作台,这里按 tab key 重定向到对应新位置,
// 让老 deep-link 不丢失语义(直接 Navigate 到 /admin/ai 会丢 tab 参数)。
const LEGACY_TAB_REDIRECT: Record<string, string> = {
  jobs: '/admin/ai/jobs',
  users: '/admin/ai/students',
  regen: '/admin/ai/courses',
  prompt: '/admin/ai', // 学科 Prompt 挪到「分类与标签」,课程 Prompt 在各课程工作台
  glossary: '/admin/ai/courses',
  providers: '/admin/settings', // Provider 挪到系统设置
};
function LegacyAIConsoleRedirect() {
  const [params] = useSearchParams();
  const tab = params.get('tab');
  const to = (tab && LEGACY_TAB_REDIRECT[tab]) || '/admin/ai';
  return <Navigate to={to} replace />;
}

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
        {/* 旧路由重定向到新工作台(对象即导航重构后,旧 AI 控制台已删):
            - ai-workflow → 任务队列(原 AIWorkflow 完整页)
            - ai-user → 学生工作台列表(原 AIUserView 按学生组织)
            - ai-console → AI 概览(旧 6 tab 控制台已拆进工作台)
            直接指向最终目标,避免双重重定向。 */}
        <Route path="ai-workflow" element={<Navigate to="/admin/ai/jobs" replace />} />
        <Route path="ai-user" element={<Navigate to="/admin/ai/students" replace />} />
        {/* 旧 AI 控制台(/admin/ai-console,6 平铺 tab)已被对象工作台取代。旧书签的
            ?tab=xxx 映射到新路径(jobs→任务队列/users→学生工作台/regen→课程工作台等),
            其余落到 AI 概览。preserveMapping 避免 deep-link 语义降级。 */}
        <Route path="ai-console" element={<LegacyAIConsoleRedirect />} />

        {/* 新:对象即导航的 AI 工作台。围绕「课程」「学生」聚合 AI 事务,消灭跨 tab 跳转。
            - /admin/ai               AI 概览(任务枢纽)
            - /admin/ai/courses       课程工作台入口(选课)
            - /admin/ai/course/:id    某门课的 AI 工作台(内容/Prompt/术语/质量)
            - /admin/ai/students      学生工作台入口(选学生)
            - /admin/ai/student/:id   某学生的 AI 工作台(题库/错题/操作)
            - /admin/ai/jobs          任务队列(原 AIWorkflow 完整页) */}
        <Route path="ai" element={<AIOverview />} />
        <Route path="ai/courses" element={<CourseWorkbenchList />} />
        <Route path="ai/course/:courseId" element={<CourseWorkbench />} />
        <Route path="ai/students" element={<StudentWorkbenchList />} />
        <Route path="ai/student/:userId" element={<StudentWorkbench />} />
        <Route path="ai/jobs" element={<AIWorkflow />} />
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
