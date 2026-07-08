import { describe, it, expect } from 'vitest';
import { sortBy, timeValue, type SortOption } from './sort';

interface Item {
  name: string;
  n: number;
  when?: string;
}

const opts: Record<string, SortOption<Item>> = {
  name: { key: 'name', label: '名', value: (i) => i.name },
  n: { key: 'n', label: '数', value: (i) => i.n },
  when: { key: 'when', label: '时', value: (i) => timeValue(i.when) },
};

const data: Item[] = [
  { name: '香蕉', n: 3, when: '2026-01-03T00:00:00Z' },
  { name: 'apple', n: 10, when: '2026-01-01T00:00:00Z' },
  { name: '苹果', n: 1, when: undefined },
];

describe('sortBy', () => {
  it('sorts strings symmetrically (desc is the exact reverse of asc)', () => {
    const asc = sortBy(data, opts.name, 'asc');
    const desc = sortBy(data, opts.name, 'desc');
    expect(desc).toEqual(asc.slice().reverse());
  });

  it('sorts numbers asc and desc', () => {
    expect(sortBy(data, opts.n, 'asc').map((i) => i.n)).toEqual([1, 3, 10]);
    expect(sortBy(data, opts.n, 'desc').map((i) => i.n)).toEqual([10, 3, 1]);
  });

  it('pushes undefined values to the bottom regardless of direction', () => {
    expect(sortBy(data, opts.when, 'asc').map((i) => i.when === undefined)).toEqual([false, false, true]);
    expect(sortBy(data, opts.when, 'desc').map((i) => i.when === undefined)).toEqual([false, false, true]);
  });

  it('does not mutate the input array', () => {
    const copy = data.slice();
    sortBy(data, opts.n, 'asc');
    expect(data).toEqual(copy);
  });
});

describe('timeValue', () => {
  it('returns undefined for empty/invalid input', () => {
    expect(timeValue(undefined)).toBeUndefined();
    expect(timeValue('')).toBeUndefined();
    expect(timeValue('not-a-date')).toBeUndefined();
  });

  it('returns epoch ms for valid ISO strings', () => {
    expect(timeValue('2026-01-01T00:00:00Z')).toBe(new Date('2026-01-01T00:00:00Z').getTime());
  });
});
