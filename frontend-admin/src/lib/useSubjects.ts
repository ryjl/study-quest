import { useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './api';
import { setSubjectCache, type SubjectMeta } from './types';

// useSubjects — fetches the DB-driven subject catalog and mirrors it into the
// module-level cache in types.ts, so subjectMeta(key) works outside React
// render paths (and before the first paint resolves).
//
// Call the no-arg form `useSubjects()` once near the app root (Layout does
// this) to warm the cache for every page; individual components can also call
// it to read the live list.
export function useSubjects() {
  const query = useQuery<SubjectMeta[]>({
    queryKey: ['subjects'],
    queryFn: async () => {
      const list = await api.listSubjects();
      setSubjectCache(list);
      return list;
    },
    staleTime: 60_000,
  });

  // Keep the cache in sync whenever the data changes (covers mutations that
  // invalidate ['subjects'] and re-fetch).
  useEffect(() => {
    if (query.data) setSubjectCache(query.data);
  }, [query.data]);

  return query;
}

// Helper for mutations to invalidate the catalog after create/update/delete.
export function useInvalidateSubjects() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ['subjects'] });
}
