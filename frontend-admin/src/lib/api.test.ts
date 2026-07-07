import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { api, ApiError } from './api';

// Verify the API client correctly parses server responses and surfaces errors
// with the right status code + message.

describe('api client', () => {
  const realFetch = global.fetch;

  beforeEach(() => {
    vi.restoreAllMocks();
  });
  afterEach(() => {
    global.fetch = realFetch;
  });

  function mockFetch(opts: { status?: number; body?: unknown; rawText?: string }) {
    const status = opts.status ?? 200;
    const text = opts.rawText ?? (opts.body !== undefined ? JSON.stringify(opts.body) : '');
    global.fetch = vi.fn().mockResolvedValue({
      ok: status >= 200 && status < 300,
      status,
      text: () => Promise.resolve(text),
    } as unknown as Response);
  }

  it('login sends POST with JSON body and cookie credentials', async () => {
    let captured: { url?: string; init?: RequestInit } = {};
    global.fetch = vi.fn().mockImplementation((url, init) => {
      captured = { url: url as string, init };
      return Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve('{"status":"ok"}') });
    });

    const res = await api.login('hunter2');
    expect(res).toEqual({ status: 'ok' });
    expect(captured.url).toBe('/admin/api/login');
    expect(captured.init?.method).toBe('POST');
    expect((captured.init as RequestInit).credentials).toBe('same-origin');
    expect(JSON.parse((captured.init as RequestInit).body as string)).toEqual({ password: 'hunter2' });
  });

  it('throws ApiError with server message on failure', async () => {
    mockFetch({ status: 401, body: { error: '密码错误' } });
    await expect(api.login('wrong')).rejects.toMatchObject({ status: 401, message: '密码错误' });
    await expect(api.login('wrong')).rejects.toBeInstanceOf(ApiError);
  });

  it('falls back to HTTP status when body has no error/message', async () => {
    mockFetch({ status: 500, body: {} });
    await expect(api.listCourses()).rejects.toMatchObject({ status: 500, message: 'HTTP 500' });
  });

  it('handles empty 204-style responses', async () => {
    mockFetch({ status: 200, rawText: '' });
    const res = await api.me();
    expect(res).toBeNull();
  });

  it('listCourses returns the parsed array', async () => {
    mockFetch({ body: [{ id: 1, title: 'X' }] });
    const res = await api.listCourses();
    expect(res).toEqual([{ id: 1, title: 'X' }]);
  });

  it('dashboardStats returns the stats object', async () => {
    const stats = {
      user_count: 3,
      course_count: 2,
      episode_count: 10,
      total_duration_seconds: 3600,
      pending_probe_count: 1,
      subject_distribution: [{ subject: 'math', count: 5 }],
      recent_daily_episodes: [{ date: '2026-07-06', count: 2 }],
    };
    mockFetch({ body: stats });
    const res = await api.dashboardStats();
    expect(res).toEqual(stats);
  });

  it('deleteEpisode sends DELETE', async () => {
    let capturedInit: RequestInit | undefined;
    global.fetch = vi.fn().mockImplementation((_url, init) => {
      capturedInit = init;
      return Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve('{"status":"deleted"}') });
    });
    await api.deleteEpisode(42);
    expect(capturedInit?.method).toBe('DELETE');
  });

  it('reorderEpisodes sends the ids array', async () => {
    let capturedBody: string | undefined;
    global.fetch = vi.fn().mockImplementation((_url, init) => {
      capturedBody = (init as RequestInit).body as string;
      return Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve('{"status":"reordered"}') });
    });
    await api.reorderEpisodes([3, 1, 2]);
    expect(JSON.parse(capturedBody!)).toEqual({ ids: [3, 1, 2] });
  });
});
