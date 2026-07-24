import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { api } from '../api';

// Verify the exam api client hits the right URL and parses the response.
// Mirrors wrongbook.test.ts's global.fetch mocking pattern.

describe('exam api', () => {
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

  it('examStats GETs /admin/api/exam/stats and parses the payload', async () => {
    let capturedUrl = '';
    global.fetch = vi.fn().mockImplementation((url) => {
      capturedUrl = url as string;
      return Promise.resolve({
        ok: true,
        status: 200,
        text: () => Promise.resolve(JSON.stringify({
          total: 8,
          submitted: 5,
          avg_score: 0.72,
          this_week: 3,
          source_quality: [
            { source: 'pool', total: 10, correct: 8, rate: 0.8 },
            { source: 'generated', total: 4, correct: 1, rate: 0.25 },
          ],
        })),
      });
    });

    const stats = await api.examStats();
    expect(capturedUrl).toBe('/admin/api/exam/stats');
    expect(stats.total).toBe(8);
    expect(stats.submitted).toBe(5);
    expect(stats.avg_score).toBeCloseTo(0.72);
    expect(stats.source_quality[0].source).toBe('pool');
    expect(stats.source_quality[0].rate).toBeCloseTo(0.8);
    expect(stats.source_quality[1].source).toBe('generated');
  });

  it('examStats handles zero-value (AI off) response', async () => {
    mockFetch({
      total: 0, submitted: 0, avg_score: 0, this_week: 0, source_quality: [],
    });
    const stats = await api.examStats();
    expect(stats.total).toBe(0);
    expect(stats.source_quality).toEqual([]);
  });

  it('examStats surfaces non-ok as ApiError', async () => {
    mockFetch({ error: 'boom' }, 500);
    await expect(api.examStats()).rejects.toThrow();
  });
});
