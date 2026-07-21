// useTypedMutation — the common "api call → toast + invalidate" skeleton.
//
// Most admin writes are simple: call an api method, show a success toast,
// invalidate one or more query keys so lists refresh, and toast any error.
// That ~10-line useMutation block was copy-pasted across Releases, Users,
// ReadingRoom, RegenTab, AIWorkflow, etc. This hook bakes in the toast-on-error
// and (optional) toast-on-success + invalidation, so a typical call site is:
//
//   const del = useTypedMutation({
//     mutationFn: api.deleteRelease,
//     successMsg: '版本已删除',
//     invalidateKeys: [['releases']],
//   });
//   <button onClick={() => del.mutate(id)}>删除</button>
//
// For mutations that need custom post-success work (optimistic updates,
// conditional invalidation, side-effect calls), pass `onSuccess` and/or
// `invalidateFn` and leave the simple fields off.

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useToast } from './toast';

export interface UseTypedMutationOptions<TVars, TRes> {
  mutationFn: (vars: TVars) => Promise<TRes>;
  /** Success toast copy. Omit to suppress the success toast. */
  successMsg?: string;
  /** Query keys to invalidate on success. Convenience for the common case. */
  invalidateKeys?: readonly (readonly unknown[])[];
  /**
   * Custom invalidation for multi-key or conditional cases. Called after the
   * success toast (if any) and before `onSuccess`. Use this when the simple
   * `invalidateKeys` list isn't expressive enough.
   */
  invalidateFn?: (qc: ReturnType<typeof useQueryClient>) => void;
  /** Optional extra success side-effect (after toast + invalidate). */
  onSuccess?: (data: TRes, vars: TVars) => void;
  /** Override the default "操作失败" error toast fallback copy. */
  errorMsg?: string;
}

export interface UseTypedMutationResult<TVars, TRes> {
  // React Query's MutateFunction already special-cases TVars=void so that
  // `.mutate()` can be called with zero arguments; we forward it unchanged.
  mutate: ReturnType<typeof useMutation<TRes, Error, TVars>>['mutate'];
  mutateAsync: ReturnType<typeof useMutation<TRes, Error, TVars>>['mutateAsync'];
  isPending: boolean;
}

// Overload 1: zero-argument mutation (mutationFn takes no params). TVars is
// fixed to void so callers write `.mutate()` with no args, matching the
// pre-refactor useMutation behavior.
export function useTypedMutation<TRes>(
  opts: UseTypedMutationOptions<void, TRes>,
): UseTypedMutationResult<void, TRes>;

// Overload 2: typed-argument mutation. `.mutate(vars)` requires the argument.
export function useTypedMutation<TVars, TRes>(
  opts: UseTypedMutationOptions<TVars, TRes>,
): UseTypedMutationResult<TVars, TRes>;

// Implementation.
export function useTypedMutation<TVars, TRes>(opts: UseTypedMutationOptions<TVars, TRes>) {
  const toast = useToast();
  const qc = useQueryClient();

  const mut = useMutation({
    mutationFn: opts.mutationFn,
    onSuccess: (data, vars) => {
      if (opts.successMsg) toast.success(opts.successMsg);
      if (opts.invalidateKeys) {
        for (const key of opts.invalidateKeys) qc.invalidateQueries({ queryKey: key });
      }
      if (opts.invalidateFn) opts.invalidateFn(qc);
      opts.onSuccess?.(data, vars);
    },
    onError: (e: unknown) => {
      // The centralized backend error mapper returns user-facing Chinese
      // messages for known cases (403/409/etc.); genuine 500s return a generic
      // message. Either way it's safe to surface directly.
      // `||` (not `??`) so an EMPTY-string message falls through to the
      // fallback too — a thrown `new Error('')` shouldn't render as a blank
      // toast.
      const msg = (e as { message?: string }).message || opts.errorMsg || '操作失败';
      toast.error(msg);
    },
  });

  return {
    mutate: mut.mutate,
    mutateAsync: mut.mutateAsync,
    isPending: mut.isPending,
  };
}
