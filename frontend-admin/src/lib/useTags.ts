import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './api';
import { setTagCache, type TagMeta } from './types';

// useTags — fetches the DB-driven tag catalog and mirrors it into the
// module-level cache in types.ts (so tagMetaByID works outside render paths).
// Layout calls this once to warm the cache for every page.
export function useTags() {
  const query = useQuery<TagMeta[]>({
    queryKey: ['tags'],
    queryFn: async () => {
      const list = await api.listTags();
      setTagCache(list);
      return list;
    },
    staleTime: 60_000,
  });

  useEffect(() => {
    if (query.data) setTagCache(query.data);
  }, [query.data]);

  return query;
}

export function useInvalidateTags() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ['tags'] });
}
