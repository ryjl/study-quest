import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api, ApiError } from '../lib/api';
import { useToast } from '../lib/toast';

export function Login() {
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();
  const { error } = useToast();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    try {
      await api.login(password);
      navigate('/admin/');
    } catch (e) {
      error((e as ApiError).message || '密码错误');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-gradient-to-br from-bg via-card-2 to-bg p-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <div className="mb-2 bg-gradient-to-r from-primary to-primary-dark bg-clip-text text-4xl font-bold text-transparent">StudyQuest</div>
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
