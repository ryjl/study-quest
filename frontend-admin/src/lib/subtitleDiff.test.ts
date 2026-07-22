import { describe, it, expect } from 'vitest';
import { parseVttCues, diffSubtitles, tokenLevelDiff } from './subtitleDiff';

// Test VTT builder: N cues with predictable text so we can assert diff output.
function vtt(cues: Array<{ start: string; end: string; text: string }>): string {
  const lines = ['WEBVTT', ''];
  cues.forEach((c, i) => {
    lines.push(String(i + 1));
    lines.push(`${c.start} --> ${c.end}`);
    lines.push(c.text);
    lines.push('');
  });
  return lines.join('\n');
}

describe('parseVttCues', () => {
  it('parses a basic VTT with multiple cues', () => {
    const s = vtt([
      { start: '00:00:00.000', end: '00:00:01.000', text: 'hello' },
      { start: '00:00:01.000', end: '00:00:02.000', text: 'world' },
    ]);
    const cues = parseVttCues(s);
    expect(cues).toHaveLength(2);
    expect(cues[0]).toMatchObject({ index: 1, start: '00:00:00.000', end: '00:00:01.000', text: 'hello' });
    expect(cues[1]).toMatchObject({ index: 2, text: 'world' });
  });

  it('skips WEBVTT header and NOTE/STYLE blocks', () => {
    const s = 'WEBVTT\n\nNOTE this is a comment\n\nSTYLE\n::cue { color: white; }\n\n1\n00:00:00.000 --> 00:00:01.000\nreal cue\n';
    const cues = parseVttCues(s);
    expect(cues).toHaveLength(1);
    expect(cues[0].text).toBe('real cue');
  });

  it('skips malformed blocks (no timestamp)', () => {
    const s = 'WEBVTT\n\njunk block without timestamp\n\n1\n00:00:00.000 --> 00:00:01.000\ngood\n';
    const cues = parseVttCues(s);
    expect(cues).toHaveLength(1);
    expect(cues[0].text).toBe('good');
  });

  it('handles cue settings on the timestamp line', () => {
    const s = 'WEBVTT\n\n1\n00:00:00.000 --> 00:00:01.000 align:middle position:50%\ntext\n';
    const cues = parseVttCues(s);
    expect(cues).toHaveLength(1);
    expect(cues[0].start).toBe('00:00:00.000');
    expect(cues[0].end).toBe('00:00:01.000');
  });

  it('accepts SRT-style comma timestamps', () => {
    const s = '1\n00:00:00,000 --> 00:00:01,000\ntext\n';
    const cues = parseVttCues(s);
    expect(cues).toHaveLength(1);
    expect(cues[0].start).toBe('00:00:00.000');
  });

  it('returns empty for empty input', () => {
    expect(parseVttCues('')).toEqual([]);
  });
});

describe('tokenLevelDiff', () => {
  it('returns empty for two empty strings', () => {
    expect(tokenLevelDiff('', '')).toEqual([]);
  });

  it('returns all-add when a is empty', () => {
    expect(tokenLevelDiff('', 'abc')).toEqual([{ type: 'add', text: 'abc' }]);
  });

  it('returns all-del when b is empty', () => {
    expect(tokenLevelDiff('abc', '')).toEqual([{ type: 'del', text: 'abc' }]);
  });

  it('returns all-same when strings are identical', () => {
    expect(tokenLevelDiff('abc', 'abc')).toEqual([{ type: 'same', text: 'abc' }]);
  });

  it('detects a single-char substitution (the polish homophone case)', () => {
    // 考算 → 口算: one char changed, one same. Should produce del+add+same
    // OR same+del+add depending on LCS tie-breaking. We assert the diff
    // reconstructs both inputs and that exactly one 'del' and one 'add' run
    // appear.
    const diff = tokenLevelDiff('考算', '口算');
    const reconstructedA = diff.filter((t) => t.type === 'same' || t.type === 'del').map((t) => t.text).join('');
    const reconstructedB = diff.filter((t) => t.type === 'same' || t.type === 'add').map((t) => t.text).join('');
    expect(reconstructedA).toBe('考算');
    expect(reconstructedB).toBe('口算');
    const dels = diff.filter((t) => t.type === 'del');
    const adds = diff.filter((t) => t.type === 'add');
    expect(dels).toHaveLength(1);
    expect(adds).toHaveLength(1);
    expect(dels[0].text).toBe('考');
    expect(adds[0].text).toBe('口');
  });

  it('detects a pure insertion', () => {
    const diff = tokenLevelDiff('abc', 'axbc');
    expect(diff).toEqual([
      { type: 'same', text: 'a' },
      { type: 'add', text: 'x' },
      { type: 'same', text: 'bc' },
    ]);
  });

  it('detects a pure deletion', () => {
    const diff = tokenLevelDiff('axbc', 'abc');
    expect(diff).toEqual([
      { type: 'same', text: 'a' },
      { type: 'del', text: 'x' },
      { type: 'same', text: 'bc' },
    ]);
  });

  it('merges consecutive same-type runs', () => {
    // 'abc' → 'adc': should merge into same/del/add/same, NOT three separate
    // 'same' entries for 'a' and 'c'.
    const diff = tokenLevelDiff('abc', 'adc');
    // Reconstruct to verify correctness regardless of tie-break order.
    const a = diff.filter((t) => t.type !== 'add').map((t) => t.text).join('');
    const b = diff.filter((t) => t.type !== 'del').map((t) => t.text).join('');
    expect(a).toBe('abc');
    expect(b).toBe('adc');
    // No two consecutive runs should have the same type (merge invariant).
    for (let i = 1; i < diff.length; i++) {
      expect(diff[i].type).not.toBe(diff[i - 1].type);
    }
  });

  it('handles CJK multibyte chars as single tokens', () => {
    // 象棋术语: 车→码 (both 1 CJK char). Each should be one token, not 3 bytes.
    const diff = tokenLevelDiff('车', '码');
    expect(diff).toEqual([
      { type: 'del', text: '车' },
      { type: 'add', text: '码' },
    ]);
  });
});

describe('diffSubtitles', () => {
  it('returns empty when raw and polished are identical', () => {
    const s = vtt([{ start: '00:00:00.000', end: '00:00:01.000', text: 'same' }]);
    expect(diffSubtitles(s, s)).toEqual([]);
  });

  it('returns only the changed cues', () => {
    const raw = vtt([
      { start: '00:00:00.000', end: '00:00:01.000', text: 'unchanged' },
      { start: '00:00:01.000', end: '00:00:02.000', text: '考算' },
      { start: '00:00:02.000', end: '00:00:03.000', text: 'also unchanged' },
    ]);
    const polished = vtt([
      { start: '00:00:00.000', end: '00:00:01.000', text: 'unchanged' },
      { start: '00:00:01.000', end: '00:00:02.000', text: '口算' },
      { start: '00:00:02.000', end: '00:00:03.000', text: 'also unchanged' },
    ]);
    const diffs = diffSubtitles(raw, polished);
    expect(diffs).toHaveLength(1);
    expect(diffs[0].index).toBe(2);
    expect(diffs[0].rawText).toBe('考算');
    expect(diffs[0].polishedText).toBe('口算');
  });

  it('captures the timestamp of the changed cue', () => {
    const raw = vtt([{ start: '00:01:30.000', end: '00:01:32.000', text: '军' }]);
    const polished = vtt([{ start: '00:01:30.000', end: '00:01:32.000', text: '车' }]);
    const diffs = diffSubtitles(raw, polished);
    expect(diffs[0].start).toBe('00:01:30.000');
    expect(diffs[0].end).toBe('00:01:32.000');
  });

  it('handles the real-world polish fix case (合不变 → 和不变)', () => {
    const raw = vtt([{ start: '00:00:05.000', end: '00:00:07.000', text: '合不变' }]);
    const polished = vtt([{ start: '00:00:05.000', end: '00:00:07.000', text: '和不变' }]);
    const diffs = diffSubtitles(raw, polished);
    expect(diffs).toHaveLength(1);
    // Token diff should show exactly the 合→和 swap with 不变 preserved.
    const dels = diffs[0].tokens.filter((t) => t.type === 'del').map((t) => t.text).join('');
    const adds = diffs[0].tokens.filter((t) => t.type === 'add').map((t) => t.text).join('');
    expect(dels).toBe('合');
    expect(adds).toBe('和');
  });
});
