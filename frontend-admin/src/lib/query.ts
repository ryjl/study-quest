// React Query refetchInterval helpers.
//
// The admin UI polls several "is work in flight?" queries: the probe scan,
// the subtitle queue stats, AI course-summary generation, and the per-user
// study-report generation. Each was written inline as
//   refetchInterval: (q) => (q.state.data?.running ? N : false)
// or the same with `status === 'generating'`. These helpers centralize that.

import type { Query } from '@tanstack/react-query';

// Poll while `data.running` is truthy. Used for the probe progress + subtitle
// stats queries where the backend exposes a boolean "running" flag.
//
// The query parameter is typed loosely (Query<any, any, any, any>) so this one
// helper slots into any useQuery regardless of TQueryFnData; we only read
// `.state.data.running` defensively.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function pollWhileActive(interval = 3000) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return (q: Query<any, any, any, any>) =>
    ((q.state.data as { running?: boolean } | undefined)?.running ? interval : false);
}

// Poll while `data.status === 'generating'`. Used for the AI course summary and
// per-user study report queries (three-state: ready / generating / '').
export function pollWhileGenerating(interval = 3000) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return (q: Query<any, any, any, any>) =>
    (((q.state.data as { status?: string } | undefined)?.status === 'generating') ? interval : false);
}

// Generic predicate form for bespoke "is anything in flight?" checks (e.g. a
// jobs list with per-item status). Returns `interval` when `pred(data)` is
// truthy, else `false` (which stops background polling). The predicate is
// handed `data` already cast to your declared type T.
export function pollWhen<T>(pred: (data: T | undefined) => boolean, interval = 3000) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return (q: Query<any, any, any, any>) => (pred(q.state.data as T | undefined) ? interval : false);
}
