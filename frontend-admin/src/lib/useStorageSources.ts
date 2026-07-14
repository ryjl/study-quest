import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './api';
import type { StorageSource } from './types';

// useStorageSources — fetches the configured storage-source catalog. Returns
// the raw react-query result (callers read .data / .isLoading). Mirrors the
// useSubjects / useTags shape but WITHOUT the module-level cache mirror —
// storage sources are only needed in render paths (Settings, Users drawer,
// Import selector), so the react-query cache is enough.
//
// Warm it once at the app root (Layout) so Settings/Import/Users share one
// request.
export function useStorageSources() {
  return useQuery<StorageSource[]>({
    queryKey: ['storage-sources'],
    queryFn: api.listStorageSources,
    staleTime: 60_000,
  });
}

// useInvalidateStorageSources — invalidate after create/update/delete so the
// list + any derived UI (Import selector, Users drawer) refetch.
export function useInvalidateStorageSources() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ['storage-sources'] });
}
