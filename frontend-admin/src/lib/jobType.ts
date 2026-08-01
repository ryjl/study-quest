// Shared job_type → Chinese label map for the AI activity feed and job tables.
//
// The backend's AIJob.job_type (and the related AIRun.capability) are free
// strings today, so this is a Record<string, string> with a passthrough
// fallback rather than a keyed union. Unified here so the three call sites
// (AIOverview, AIWorkflow, Dashboard) can't drift — they previously had three
// hand-maintained copies that had already diverged (`homework` missing in one,
// `advice`/`user_report` labelled differently in another).

export const JOB_TYPE_LABEL: Record<string, string> = {
  slice: '切片',
  summarize: '总结',
  segment: '切片',
  summary: '总结',
  quiz: '出题',
  polish: '字幕润色',
  advice: '学习建议',
  course_summary: '课程总结',
  user_report: '学习报告',
  homework: '课后作业',
};

// jobTypeLabel returns the Chinese label for a job_type/capability, or the
// raw value when no label is mapped (unknown/new job types pass through).
export function jobTypeLabel(t: string): string {
  return JOB_TYPE_LABEL[t] ?? t;
}
