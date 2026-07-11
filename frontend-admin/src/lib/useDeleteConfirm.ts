import { useMutation } from '@tanstack/react-query';
import { useConfirm, useToast } from './toast';

// useDeleteConfirm captures the delete-mutation + confirm-gate skeleton that
// was triplicated across Subjects/Tags/Badges: a useMutation (api call → toast
// on success → invalidate on success → toast on error) plus a confirm() gate
// before .mutate. Each page re-wired this ~10-line block with only the noun,
// API method, and confirm copy differing.
//
// Usage:
//   const del = useDeleteConfirm({
//     mutationFn: api.deleteSubject,
//     noun: '科目',
//     onDeleted: invalidate,
//   });
//   <button onClick={() => del.confirmAndDelete(s.id!, `删除「${s.label}」？`, '若该科目下仍有课程…')}>删除</button>

export function useDeleteConfirm<T>(opts: {
  mutationFn: (id: T) => Promise<{ status: string }>;
  noun: string; // e.g. '科目' — used in the success toast "X已删除"
  onDeleted: () => void; // typically the resource's invalidate()
}) {
  const toast = useToast();
  const confirm = useConfirm();

  const deleteMut = useMutation({
    mutationFn: opts.mutationFn,
    onSuccess: () => {
      toast.success(`${opts.noun}已删除`);
      opts.onDeleted();
    },
    onError: (e: unknown) => {
      // The centralized backend error mapper (handler/respondError) returns
      // user-facing Chinese messages for known cases (403/409); genuine 500s
      // return a generic message. Either way it's safe to surface directly.
      const msg = (e as { message?: string }).message ?? '删除失败';
      toast.error(msg);
    },
  });

  return {
    isPending: deleteMut.isPending,
    // confirmAndDelete shows the confirm dialog, then mutates if confirmed.
    async confirmAndDelete(id: T, message: string, detail?: string) {
      const ok = await confirm({ message, detail, danger: true });
      if (ok) deleteMut.mutate(id);
    },
  };
}
