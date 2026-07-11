import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './api';

// useBadges mirrors the useSubjects/useTags pattern: a query keyed on the
// resource name plus an invalidate helper. Badges.tsx previously inlined
// useQuery + useQueryClient directly, breaking the established convention.
// Note: unlike subjects/tags, badges have NO module-level cache mirror in
// types.ts (badges aren't resolved by key outside their own page), so there's
// no cache-warming useEffect here.

export function useBadges() {
  return useQuery({ queryKey: ['badges'], queryFn: api.listBadges, staleTime: 60_000 });
}

export function useInvalidateBadges() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ['badges'] });
}
