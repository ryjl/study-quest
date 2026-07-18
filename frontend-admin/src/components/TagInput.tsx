import { X } from 'lucide-react';
import { useTags } from '../lib/useTags';
import type { TagMeta } from '../lib/types';

// TagInput — multi-select over the DB-driven tag catalog. Value is the list
// of selected tag IDs; onChange emits the new ID list. Tags themselves are
// managed on the dedicated Tags admin page (no inline creation here).
export function TagInput({
  value,
  onChange,
}: {
  value: number[];
  onChange: (ids: number[]) => void;
}) {
  const tagsQ = useTags();
  const all: TagMeta[] = tagsQ.data ?? [];
  const selected = new Set(value);

  const toggle = (id: number) => {
    if (selected.has(id)) onChange(value.filter((v) => v !== id));
    else onChange([...value, id]);
  };

  const selectedTags = all.filter((t) => t.id != null && selected.has(t.id));
  const availableTags = all.filter((t) => t.id != null && !selected.has(t.id));

  return (
    <div>
      {/* Selected chips */}
      {selectedTags.length > 0 && (
        <div className="mb-2 flex flex-wrap gap-1.5">
          {selectedTags.map((t) => (
            <span
              key={t.id}
              className="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-xs"
              style={{ backgroundColor: `${t.color}25`, color: t.color }}
            >
              {t.label}
              <button
                type="button"
                onClick={() => toggle(t.id!)}
                className="opacity-60 hover:opacity-100"
                aria-label={`移除 ${t.label}`}
              >
                <X size={12} />
              </button>
            </span>
          ))}
        </div>
      )}

      {/* Available tags to pick */}
      {availableTags.length > 0 ? (
        <div className="flex flex-wrap gap-1.5">
          {availableTags.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => toggle(t.id!)}
              className="rounded-md border border-border bg-card-2 px-2 py-0.5 text-xs text-muted transition hover:border-primary hover:text-primary"
            >
              + {t.label}
            </button>
          ))}
        </div>
      ) : (
        <p className="text-xs text-muted">
          {all.length === 0
            ? '还没有标签，先到「标签管理」页创建。'
            : '已选中全部标签。'}
        </p>
      )}
    </div>
  );
}
