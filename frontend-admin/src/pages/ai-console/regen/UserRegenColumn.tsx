// UserRegenColumn — the right column of RegenTab. Pick a user via a datalist
// (nickname may be duplicated, so the option value embeds "(#id)" and we
// parse that on change rather than find-by-nickname), then render the three
// per-user cards: study report, advice, quizzes.

import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../../../lib/api';
import { UserStudyReportCard } from './UserStudyReportCard';
import { UserAdviceCard } from './UserAdviceCard';
import { UserQuizzesCard } from './UserQuizzesCard';

export function UserRegenColumn() {
  const usersQ = useQuery({ queryKey: ['users'], queryFn: api.listUsers });
  const users = usersQ.data ?? [];
  const [userId, setUserId] = useState<number | null>(null);
  const [query, setQuery] = useState('');
  const selectedUser = useMemo(() => users.find((u) => u.id === userId) ?? null, [users, userId]);

  return (
    <section className="space-y-4 rounded-lg border border-border bg-card p-4">
      <header className="space-y-0.5">
        <h2 className="text-base font-semibold">按学生操作</h2>
        <p className="text-xs text-muted">重新生成 / 删除学习报告、学习建议、题库。</p>
      </header>

      {/* 用户选择 —— 复用 AIUserView 的 datalist + "(#id)" 解析模式
          (nickname 可能重复,不能用 find 反查)。 */}
      <div>
        <label className="mb-1 block text-xs text-muted">选择学生</label>
        <input
          className="input max-w-[260px]"
          list="regen-user-options"
          placeholder={usersQ.isLoading ? '加载用户…' : '搜索昵称选择用户'}
          value={selectedUser ? `${selectedUser.nickname} (#${selectedUser.id})` : query}
          onChange={(e) => {
            const entered = e.target.value;
            const idMatch = entered.match(/\(#(\d+)\)\s*$/);
            if (idMatch) {
              const id = Number(idMatch[1]);
              if (users.some((u) => u.id === id)) {
                setUserId(id);
                setQuery('');
                return;
              }
            }
            setUserId(null);
            setQuery(entered);
          }}
        />
        <datalist id="regen-user-options">
          {users
            .filter((u) => (query ? u.nickname.toLowerCase().includes(query.toLowerCase()) : true))
            .map((u) => (
              <option key={u.id} value={`${u.nickname} (#${u.id})`}>
                {u.role}
              </option>
            ))}
        </datalist>
      </div>

      {userId != null ? (
        <>
          <UserStudyReportCard userId={userId} />
          <UserAdviceCard userId={userId} />
          <UserQuizzesCard userId={userId} />
        </>
      ) : (
        <div className="rounded-md border border-dashed border-border bg-card-2 px-4 py-8 text-center text-sm text-muted">
          选择一个学生以操作其 AI 数据。
        </div>
      )}
    </section>
  );
}
