import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import type { TagMeta } from '../lib/types';

// TagInput renders against the DB-driven tag catalog (via useTags). We mock
// useTags to read from a mutable catalog variable so individual tests can vary
// it without needing a QueryClient provider or network. The component was
// refactored from the old free-text "type a comma-separated string" input to
// an ID-based multi-select over the catalog — these tests cover the current
// behavior (toggle chips, no duplicates, empty-catalog hint), not the legacy
// free-text behavior.
//
// vi.mock is hoisted by vitest, so it runs before the import below; the
// factory reads `catalog` via the closure vitest provides (the `vi.hoisted`
// indirection keeps the variable reference stable across the hoist boundary).
const { catalog, setCatalog } = vi.hoisted(() => {
  let c: TagMeta[] = [];
  return { catalog: () => c, setCatalog: (v: TagMeta[]) => { c = v; } };
});

vi.mock('../lib/useTags', () => ({
  useTags: () => ({ data: catalog(), isLoading: false }),
  useInvalidateTags: () => () => {},
}));

const TAGS: TagMeta[] = [
  { id: 1, key: 'required', label: '必修', color: '#ef4444' },
  { id: 2, key: 'thinking', label: '思维训练', color: '#f59e0b' },
  { id: 3, key: 'extra', label: '拓展', color: '#10b981' },
];

// Import AFTER the vi.mock above is registered so the mock takes effect.
import { TagInput } from './TagInput';

describe('TagInput', () => {
  beforeEach(() => {
    setCatalog(TAGS);
  });

  it('renders selected tags as chips with their labels', () => {
    render(<TagInput value={[1, 2]} onChange={() => {}} />);
    expect(screen.getByText('必修')).toBeInTheDocument();
    expect(screen.getByText('思维训练')).toBeInTheDocument();
  });

  it('lists unselected catalog tags as + buttons', () => {
    render(<TagInput value={[1]} onChange={() => {}} />);
    // 必修 is selected (chip); the other two are available as + buttons.
    expect(screen.getByText('+ 思维训练')).toBeInTheDocument();
    expect(screen.getByText('+ 拓展')).toBeInTheDocument();
  });

  it('adds a tag id when its + button is clicked', () => {
    const onChange = vi.fn();
    render(<TagInput value={[1]} onChange={onChange} />);
    fireEvent.click(screen.getByText('+ 思维训练'));
    expect(onChange).toHaveBeenCalledWith([1, 2]);
  });

  it('removes a tag id when the chip × is clicked', () => {
    const onChange = vi.fn();
    render(<TagInput value={[1, 2]} onChange={onChange} />);
    fireEvent.click(screen.getByLabelText('移除 必修'));
    expect(onChange).toHaveBeenCalledWith([2]);
  });

  it('does not render a selected tag again as an available + button', () => {
    render(<TagInput value={[1]} onChange={() => {}} />);
    // 必修 is selected → it must NOT also appear as a "+ 必修" button.
    expect(screen.queryByText('+ 必修')).not.toBeInTheDocument();
  });

  it('shows the empty-catalog hint when the catalog is empty', () => {
    setCatalog([]);
    // cleanup to drop the prior render before re-rendering in this case.
    cleanup();
    render(<TagInput value={[]} onChange={() => {}} />);
    expect(screen.getByText(/还没有标签/)).toBeInTheDocument();
  });
});
