// StorageWhitelistSection renders the user's storage-source whitelist (防呆)
// as a checkbox list. Semantics: an EMPTY list means default-deny (the user
// is allowed NO sources — any content that lives on a storage source is
// refused at grant time; see backend admin_storage_gate.go). The admin must
// grant at least one source before the user can stream imported content.
// The whole list is replaced on every toggle via setStorageWhitelist (PUT).
// The catalog comes from useStorageSources (warmed at the app root).

import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import type { User } from '../../lib/types';
import { useStorageSources } from '../../lib/useStorageSources';
import { useToast } from '../../lib/toast';
import { Database } from 'lucide-react';

export function StorageWhitelistSection({
  userId,
  current,
}: {
  userId: number;
  current: number[];
}) {
  const sourcesQ = useStorageSources();
  const qc = useQueryClient();
  const toast = useToast();
  const selected = new Set(current);
  const sources = sourcesQ.data ?? [];

  // Optimistic update: patch the cached ['users'] entry's storage_source_access
  // BEFORE the PUT resolves, so a rapid second toggle reads the just-clicked
  // baseline instead of the stale server snapshot (which would otherwise lose
  // the first toggle to last-write-wins). onMutate returns the previous cache
  // so onError can roll back.
  const mut = useMutation({
    mutationFn: (ids: number[]) => api.setStorageWhitelist(userId, ids),
    onMutate: async (nextIds: number[]) => {
      await qc.cancelQueries({ queryKey: ['users'] });
      const prev = qc.getQueryData<User[]>(['users']);
      qc.setQueryData<User[]>(['users'], (old) =>
        (old ?? []).map((u) => (u.id === userId ? { ...u, storage_source_access: nextIds } : u)),
      );
      return { prev };
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (e, _ids, ctx) => {
      if (ctx?.prev) qc.setQueryData(['users'], ctx.prev);
      toast.error((e as Error).message);
    },
  });

  const toggle = (id: number, on: boolean) => {
    const next = on ? [...selected, id] : [...selected].filter((x) => x !== id);
    mut.mutate(next);
  };

  return (
    <section className="mb-6">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="flex items-center gap-1.5 text-sm font-semibold text-txt">
          <Database size={14} className="text-muted" />
          允许的存储源 ({selected.size}/{sources.length})
        </h3>
        {selected.size > 0 && (
          <button className="btn-danger btn-sm" onClick={() => mut.mutate([])} disabled={mut.isPending}>
            清空（全拒）
          </button>
        )}
      </div>
      <p className="mb-2 text-xs text-muted">
        防呆：勾选后该用户只能访问这些源的内容。<strong>空 = 一个都不允许</strong>（必须勾选至少一个源，用户才能播放任何内容）。
      </p>
      {sources.length === 0 ? (
        <p className="rounded-lg border border-border bg-card-2 px-3 py-2 text-xs text-muted">
          尚未配置存储源（在「系统设置」新增）。
        </p>
      ) : (
        <div className="max-h-48 space-y-1 overflow-auto">
          {sources.map((s) => (
            <label
              key={s.id}
              className="flex items-center gap-2 rounded-lg border border-border bg-card-2 px-3 py-1.5 text-sm"
            >
              <input
                type="checkbox"
                checked={selected.has(s.id!)}
                onChange={(e) => toggle(s.id!, e.target.checked)}
                className="h-4 w-4 accent-primary"
              />
              <span className="flex-1 text-txt">
                {s.name}
                {s.is_default && <span className="ml-1 text-[10px] text-primary">默认</span>}
              </span>
              <span className="text-[10px] uppercase text-muted">{s.type}</span>
            </label>
          ))}
        </div>
      )}
    </section>
  );
}
