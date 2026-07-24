import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { api } from '../api';

// Verify the wrong-book api client hits the right URL and parses the response.
// Mirrors api.test.ts's global.fetch mocking pattern.

describe('wrongbook api', () => {
  const realFetch = global.fetch;

  beforeEach(() => {
    vi.restoreAllMocks();
  });
  afterEach(() => {
    global.fetch = realFetch;
  });

  function mockFetch(body: unknown, status = 200) {
    global.fetch = vi.fn().mockResolvedValue({
      ok: status >= 200 && status < 300,
      status,
      text: () => Promise.resolve(JSON.stringify(body)),
    } as unknown as Response);
  }

  it('wrongBookStats GETs /admin/api/wrong-book/stats and parses the payload', async () => {
    let capturedUrl = '';
    global.fetch = vi.fn().mockImplementation((url) => {
      capturedUrl = url as string;
      return Promise.resolve({
        ok: true,
        status: 200,
        text: () => Promise.resolve(JSON.stringify({
          total: 12,
          unmastered: 5,
          this_week: 3,
          master_rate: 0.583,
          top_frequent: [{ question_id: 7, stem: 'q7', occur_count: 4, total_attempts: 9 }],
          by_subject: [{ subject_key: 'math', subject_label: '数学', count: 12 }],
        })),
      });
    });

    const stats = await api.wrongBookStats();
    expect(capturedUrl).toBe('/admin/api/wrong-book/stats');
    expect(stats.total).toBe(12);
    expect(stats.unmastered).toBe(5);
    expect(stats.master_rate).toBeCloseTo(0.583);
    expect(stats.top_frequent[0].question_id).toBe(7);
    expect(stats.by_subject[0].subject_label).toBe('数学');
  });

  it('wrongBookStats handles zero-value (AI off) response', async () => {
    mockFetch({
      total: 0, unmastered: 0, this_week: 0, master_rate: 0,
      top_frequent: [], by_subject: [],
    });
    const stats = await api.wrongBookStats();
    expect(stats.total).toBe(0);
    expect(stats.top_frequent).toEqual([]);
    expect(stats.by_subject).toEqual([]);
  });

  it('wrongBookStats surfaces non-ok as ApiError', async () => {
    mockFetch({ error: 'boom' }, 500);
    await expect(api.wrongBookStats()).rejects.toThrow();
  });
});
