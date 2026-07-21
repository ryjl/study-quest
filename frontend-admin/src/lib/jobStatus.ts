// Shared job-status metadata for the SubtitleQueue and AIWorkflow pages.
//
// SubtitleJobStatus and AiJobStatus are the same union
//   'queued' | 'processing' | 'done' | 'failed' | 'skipped'
// (see types.ts), so a single palette + filter list covers both. The only
// page-specific wrinkle is the `processing` label: subtitles call it "转录中"
// (transcription) and the AI console uses the generic "处理中". The subtitle
// page imports SUBTITLE_STATUS_META which overrides that one label.

import type { AiJobStatus, SubtitleJobStatus } from './types';

// JobStatus is the shared union (the two named aliases are structurally
// identical). We type STATUS_META against the AiJobStatus alias purely so
// callers get a stable name; the values apply equally to SubtitleJobStatus.
export type JobStatus = AiJobStatus | SubtitleJobStatus;

export interface StatusMeta {
  label: string;
  cls: string;
}

// Default palette used by the AI console. `bad` / `muted` tokens track the
// theme, matching the inline definitions these pages had before.
export const STATUS_META: Record<JobStatus, StatusMeta> = {
  queued: { label: '排队中', cls: 'bg-blue-500/15 text-blue-600' },
  processing: { label: '处理中', cls: 'bg-amber-500/15 text-amber-600' },
  done: { label: '已完成', cls: 'bg-emerald-500/15 text-emerald-600' },
  failed: { label: '失败', cls: 'bg-bad/15 text-bad' },
  skipped: { label: '已跳过', cls: 'bg-gray-500/15 text-muted' },
};

// Subtitle-specific palette: "转录中" reads better than the generic "处理中"
// on the subtitle queue. Same color classes; just the one label overridden.
export const SUBTITLE_STATUS_META: Record<JobStatus, StatusMeta> = {
  ...STATUS_META,
  processing: { label: '转录中', cls: 'bg-amber-500/15 text-amber-600' },
};

// Canonical status filter order for the queue/console tab bars.
export const STATUS_FILTERS: (JobStatus | 'all')[] = [
  'all',
  'queued',
  'processing',
  'done',
  'failed',
  'skipped',
];
