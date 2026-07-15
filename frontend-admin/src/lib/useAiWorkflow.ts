import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './api';
import type { AiRun } from './types';

// useAiJobs — fetches the AI workflow job list + rolled-up status counts.
// The list endpoint serves both jobs and stats in one payload, so we read
// .data?.jobs / .data?.stats. Polling is driven by the caller (the page
// passes refetchInterval based on whether any job is active) rather than here,
// because the "is anything in-flight?" check needs the data we're fetching.
export function useAiJobs(jobType?: string, status?: string) {
  return useQuery({
    queryKey: ['ai-jobs', jobType ?? null, status ?? null],
    queryFn: () => api.listAiJobs(jobType, status),
  });
}

// useInvalidateAiJobs — invalidate after enqueue (and any future write) so the
// list + stats refetch immediately. Mirrors useInvalidateAiProviders.
export function useInvalidateAiJobs() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ['ai-jobs'] });
}

// useAiRuns — the most recent model-invocation traces (decision replay). limit
// defaults to 20; the page keeps it small so the trace list stays scannable.
export function useAiRuns(limit = 20) {
  return useQuery<AiRun[]>({
    queryKey: ['ai-runs', limit],
    queryFn: () => api.listAiRuns(limit),
  });
}
