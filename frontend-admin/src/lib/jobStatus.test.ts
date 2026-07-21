import { describe, it, expect } from 'vitest';
import { STATUS_META, SUBTITLE_STATUS_META, STATUS_FILTERS, type JobStatus } from './jobStatus';

// Shared job-status metadata for the SubtitleQueue and AIWorkflow pages.
// Both pages were using their own byte-identical copies before consolidation.

describe('STATUS_META', () => {
  it('has an entry for every status in the union', () => {
    const statuses: JobStatus[] = ['queued', 'processing', 'done', 'failed', 'skipped'];
    for (const s of statuses) {
      expect(STATUS_META[s]).toBeTruthy();
      expect(typeof STATUS_META[s].label).toBe('string');
      expect(typeof STATUS_META[s].cls).toBe('string');
      expect(STATUS_META[s].label.length).toBeGreaterThan(0);
    }
  });

  it('uses Chinese labels', () => {
    expect(STATUS_META.queued.label).toBe('排队中');
    expect(STATUS_META.processing.label).toBe('处理中');
    expect(STATUS_META.done.label).toBe('已完成');
    expect(STATUS_META.failed.label).toBe('失败');
    expect(STATUS_META.skipped.label).toBe('已跳过');
  });
});

describe('SUBTITLE_STATUS_META', () => {
  it('matches STATUS_META except for the processing label (转录中 vs 处理中)', () => {
    // Same labels for 4 of 5 statuses...
    expect(SUBTITLE_STATUS_META.queued).toEqual(STATUS_META.queued);
    expect(SUBTITLE_STATUS_META.done).toEqual(STATUS_META.done);
    expect(SUBTITLE_STATUS_META.failed).toEqual(STATUS_META.failed);
    expect(SUBTITLE_STATUS_META.skipped).toEqual(STATUS_META.skipped);
    // ...but the subtitle queue overrides "处理中" → "转录中".
    expect(SUBTITLE_STATUS_META.processing.label).toBe('转录中');
    expect(STATUS_META.processing.label).toBe('处理中');
    // Same color class either way.
    expect(SUBTITLE_STATUS_META.processing.cls).toBe(STATUS_META.processing.cls);
  });
});

describe('STATUS_FILTERS', () => {
  it('lists "all" first then the 5 statuses in a stable canonical order', () => {
    expect(STATUS_FILTERS).toEqual([
      'all',
      'queued',
      'processing',
      'done',
      'failed',
      'skipped',
    ]);
  });

  it('includes every status that has a STATUS_META entry', () => {
    const filterStatuses = STATUS_FILTERS.filter((f): f is JobStatus => f !== 'all');
    const metaStatuses = Object.keys(STATUS_META) as JobStatus[];
    expect(filterStatuses.sort()).toEqual(metaStatuses.sort());
  });
});
