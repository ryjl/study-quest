import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './api';
import type { AiProvider } from './types';

// useAiProviders — fetches the configured AI-provider catalog. Returns the raw
// react-query result (callers read .data / .isLoading). Mirrors the
// useStorageSources shape: AI providers are only needed in render paths
// (Settings), so the react-query cache is enough — no module-level mirror.
export function useAiProviders() {
  return useQuery<AiProvider[]>({
    queryKey: ['ai-providers'],
    queryFn: api.listAiProviders,
    staleTime: 60_000,
  });
}

// useInvalidateAiProviders — invalidate after create/update/delete so the list
// refetches.
export function useInvalidateAiProviders() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ['ai-providers'] });
}
