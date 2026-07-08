// Generic display-sorting helpers for the admin list views.
//
// These are PURE DISPLAY sorts — they re-order an already-loaded list in
// memory without writing back to the backend. A separate explicit "save as
// order" action (calling the existing reorder endpoint) is what persists a
// new sort_order; that lives in the components that need it.

export type SortDir = 'asc' | 'desc';

export interface SortOption<T> {
  key: string; // stable identifier stored in component state
  label: string; // dropdown text
  // value extracts the comparable from one item (string | number | Date-ish).
  value: (item: T) => string | number | undefined;
}

/**
 * Sorts a copy of `items` by the given option + direction. Undefined values
 * always sort last regardless of direction (so items missing a field don't
 * leap to the top). String comparison is locale-aware for Chinese titles.
 */
export function sortBy<T>(items: T[], opt: SortOption<T>, dir: SortDir): T[] {
  const out = items.slice();
  const collator = new Intl.Collator('zh-Hans', { numeric: true, sensitivity: 'base' });
  out.sort((a, b) => {
    const va = opt.value(a);
    const vb = opt.value(b);
    // undefined always sinks to the bottom.
    if (va === undefined && vb === undefined) return 0;
    if (va === undefined) return 1;
    if (vb === undefined) return -1;
    let cmp: number;
    if (typeof va === 'number' && typeof vb === 'number') {
      cmp = va - vb;
    } else {
      cmp = collator.compare(String(va), String(vb));
    }
    return dir === 'asc' ? cmp : -cmp;
  });
  return out;
}

/** Parses an ISO timestamp into a number for date-based sorting (NaN → undefined). */
export function timeValue(iso?: string): number | undefined {
  if (!iso) return undefined;
  const t = new Date(iso).getTime();
  return Number.isNaN(t) ? undefined : t;
}
