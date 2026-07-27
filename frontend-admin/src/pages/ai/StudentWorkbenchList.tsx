// StudentWorkbenchList — 学生工作台入口页(选哪个学生进入他的 AI 工作台)。
// 复用用户列表,每行加"进入 AI 工作台"入口。简洁中转页,不做授权等复杂操作
// (那是「用户与授权」页的职责)。
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ChevronRight } from 'lucide-react';
import { api } from '../../lib/api';
import { PageHeader } from '../../components/PageHeader';
import { Spinner } from '../../components/ui';
import { ROLES } from '../users/Users';

export function StudentWorkbenchList() {
  const navigate = useNavigate();
  const usersQ = useQuery({ queryKey: ['users'], queryFn: api.listUsers });

  if (usersQ.isLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <Spinner size={24} />
      </div>
    );
  }

  const users = usersQ.data ?? [];

  return (
    <div>
      <PageHeader
        title="学生工作台"
        breadcrumb={[{ label: 'AI 运营' }]}
        description="选择一个学生,进入他的 AI 工作台 —— 题库、错题、学习报告与重新生成操作。"
      />
      {users.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border bg-card px-4 py-12 text-center text-sm text-muted">
          还没有用户。
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {users.map((u) => (
            <button
              key={u.id}
              onClick={() => navigate(`/admin/ai/student/${u.id}`)}
              className="group flex items-center gap-3 rounded-lg border border-border bg-card p-3 text-left transition-colors hover:border-primary/40 hover:bg-card-2/50"
            >
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary">
                {u.nickname.slice(0, 1)}
              </span>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium text-txt">{u.nickname}</div>
                <div className="text-[11px] text-muted">
                  {/* role 本地化:后端返回英文 key(student/parent/...),用 ROLES 表映射成中文。 */}
                  {ROLES.find((r) => r.key === u.role)?.label ?? u.role}
                  <span className="ml-1 text-muted/60">#{u.id}</span>
                </div>
              </div>
              <ChevronRight size={16} className="shrink-0 text-muted opacity-0 transition-opacity group-hover:opacity-100" />
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
