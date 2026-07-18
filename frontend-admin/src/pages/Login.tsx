import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { api, ApiError } from '../lib/api';
import { useToast } from '../lib/toast';

export function Login() {
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();
  const { error } = useToast();
  const qc = useQueryClient();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    try {
      await api.login(password);
      // CRITICAL: invalidate the ['me'] cache so AuthGuard refetches. The boot
      // check (on first visit to /admin) caches authenticated:false for up to
      // staleTime (10s). Without this, navigating to /admin/ after a successful
      // login reads that stale false and bounces back to /admin/login — which
      // is exactly the "first few clicks do nothing, eventually get in" bug.
      // We also set the optimistic value so the very first render after
      // navigation already sees authenticated:true (no flash of the login page).
      qc.setQueryData(['me'], { authenticated: true });
      await qc.invalidateQueries({ queryKey: ['me'] });
      navigate('/admin/');
    } catch (e) {
      error((e as ApiError).message || '密码错误');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-bg p-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <div className="mb-3 flex items-center justify-center gap-2">
            <div className="h-8 w-8 rounded-md bg-primary flex items-center justify-center text-bg font-bold">S</div>
          </div>
          <div className="text-3xl font-semibold tracking-tight text-txt">StudyQuest</div>
          <div className="text-sm text-muted">学途奇旅后台管理系统</div>
        </div>
        <form onSubmit={submit} className="card">
          <label className="mb-1 block text-sm text-muted">管理员密码</label>
          <input
            type="password"
            className="input mb-4"
            placeholder="请输入管理员密码"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoFocus
            required
          />
          <button type="submit" className="btn-primary w-full" disabled={loading || !password}>
            {loading ? '登录中...' : '登录'}
          </button>
        </form>
        <p className="mt-4 text-center text-xs text-muted">默认密码：admin（首次启动时自动创建）</p>
      </div>
    </div>
  );
}
