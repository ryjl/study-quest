// Subtitle diff utilities for the admin "subtitle version" UI.
//
// The polish pipeline overwrites VttContent with corrected text but keeps the
// immutable pre-polish snapshot in RawVttContent (see model.Subtitle doc). This
// module turns two VTT strings (raw + polished) into a structured diff the UI
// can render as: (a) a list of changed cues with line-level background tint,
// and (b) within each changed cue, a token-level +/- color block showing
// exactly which characters the LLM added/removed/changed.
//
// The token-level diff is a plain LCS over CHARACTERS (not words) — polish
// corrections are typically single-character homophone swaps (军→车, 考算→口算),
// so word-level diffing would miss most of them. Char-level catches every
// substitution. The cost is O(n*m) per cue, but cues are short (dozens of
// chars), so this is cheap.

/** One cue parsed out of a VTT string. Index is 1-based (matches SRT/VTT numbering). */
export interface ParsedCue {
  index: number;
  start: string;
  end: string;
  text: string;
}

/** One piece of a token-level diff: a run of characters all of the same type. */
export interface TokenDiff {
  type: 'same' | 'add' | 'del';
  text: string;
}

/** One cue that differs between raw and polished, with its token-level diff. */
export interface CueDiff {
  index: number;
  start: string;
  end: string;
  rawText: string;
  polishedText: string;
  tokens: TokenDiff[];
}

/**
 * Parse a VTT string into a list of cues. Tolerant of the common WebVTT shape
 * (WEBVTT header, optional NOTE/STYLE blocks, cue blocks with optional cue
 * settings on the timestamp line). Returns cues in document order, indexed
 * 1-based to match the SRT/VTT numbering the admin sees in the player.
 *
 * Malformed blocks (missing timestamp, unparseable times) are skipped silently
 * rather than throwing — a single bad cue shouldn't break the whole diff view.
 */
export function parseVttCues(vtt: string): ParsedCue[] {
  if (!vtt) return [];
  const cues: ParsedCue[] = [];
  // Normalize line endings, then split into blocks on blank lines. Trim leading
  // BOM/whitespace so a leading "\n\n" doesn't produce an empty first block.
  const normalized = vtt.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
  const blocks = normalized.split(/\n\s*\n/);
  let cueIndex = 0;
  for (const block of blocks) {
    const lines = block.split('\n').filter((l) => l.length > 0);
    if (lines.length === 0) continue;
    // Skip the WEBVTT header and NOTE/STYLE/REGION blocks outright.
    const firstLower = lines[0].toLowerCase();
    if (firstLower === 'webvtt' || firstLower.startsWith('webvtt ')) continue;
    if (firstLower === 'note' || firstLower === 'style' || firstLower === 'region') continue;
    // Find the timestamp line: the first line matching "-->". Lines before it
    // are either a cue identifier (VTT "1" / "cue-xyz") or junk.
    let tsLineIdx = -1;
    for (let i = 0; i < lines.length; i++) {
      if (lines[i].includes('-->')) {
        tsLineIdx = i;
        break;
      }
    }
    if (tsLineIdx === -1) continue; // no timestamp = not a cue block
    const tsLine = lines[tsLineIdx];
    const times = parseTimestampLine(tsLine);
    if (!times) continue;
    // Text = all lines after the timestamp line, joined with \n (preserves
    // multi-line cue text shape for the raw view).
    const text = lines.slice(tsLineIdx + 1).join('\n');
    cueIndex++;
    cues.push({ index: cueIndex, start: times.start, end: times.end, text });
  }
  return cues;
}

/**
 * Parse a VTT timestamp line like "00:00:01.000 --> 00:00:02.000 align:middle"
 * into {start, end} string labels. Returns null if either timestamp fails to
 * parse. The returned strings are the raw timestamp portions (no cue settings),
 * suitable for display.
 */
function parseTimestampLine(line: string): { start: string; end: string } | null {
  // Strip cue settings (anything after the second timestamp).
  const m = line.match(/(\d{2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}:\d{2},\d{3})\s*-->\s*(\d{2}:\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}\.\d{3}|\d{2}:\d{2}:\d{2},\d{3})/);
  if (!m) return null;
  return { start: m[1].replace(',', '.'), end: m[2].replace(',', '.') };
}

/**
 * Compute the diff between two VTT strings. Cues are aligned by 1-based index
 * (both raw and polished come from the same segmenter, so cue N in raw corresponds
 * to cue N in polished). Returns only the cues whose text actually changed —
 * unchanged cues are omitted to keep the diff view focused.
 *
 * If the two strings have different cue counts (which shouldn't happen in normal
 * polish flow — polish preserves timestamps — but could happen if the admin
 * manually edited), we diff up to the shorter length and ignore the tail.
 */
export function diffSubtitles(rawVtt: string, polishedVtt: string): CueDiff[] {
  const raw = parseVttCues(rawVtt);
  const polished = parseVttCues(polishedVtt);
  const out: CueDiff[] = [];
  const n = Math.min(raw.length, polished.length);
  for (let i = 0; i < n; i++) {
    const r = raw[i];
    const p = polished[i];
    if (r.text === p.text) continue; // unchanged
    out.push({
      index: p.index,
      start: p.start,
      end: p.end,
      rawText: r.text,
      polishedText: p.text,
      tokens: tokenLevelDiff(r.text, p.text),
    });
  }
  return out;
}

/**
 * Character-level LCS diff between two strings. Returns a sequence of
 * {type, text} runs where type is 'same' (in both), 'add' (only in b), or 'del'
 * (only in a). Consecutive same-type chars are merged into a single run.
 *
 * Algorithm: standard dynamic-programming LCS table, then backtrack to produce
 * the diff. O(n*m) time and space. For subtitle cues (tens to low hundreds of
 * chars) this is fast enough; if we ever diff whole episodes at once we'd want
 * Myers diff, but the per-cue granularity keeps inputs small.
 *
 * The diff is computed over the Array.from(string) of CHARACTERS (not code
 * units), so multi-byte CJK characters and emoji each count as one token.
 */
export function tokenLevelDiff(a: string, b: string): TokenDiff[] {
  const aa = Array.from(a);
  const bb = Array.from(b);
  const la = aa.length;
  const lb = bb.length;
  if (la === 0 && lb === 0) return [];
  if (la === 0) return [{ type: 'add', text: b }];
  if (lb === 0) return [{ type: 'del', text: a }];

  // dp[i][j] = LCS length of aa[:i] and bb[:j].
  const dp: number[][] = Array.from({ length: la + 1 }, () => new Array(lb + 1).fill(0));
  for (let i = 1; i <= la; i++) {
    for (let j = 1; j <= lb; j++) {
      if (aa[i - 1] === bb[j - 1]) {
        dp[i][j] = dp[i - 1][j - 1] + 1;
      } else {
        dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]);
      }
    }
  }
  // Backtrack from (la, lb) to (0, 0), emitting tokens in REVERSE order, then
  // reverse at the end. A diagonal move = same char; up move = del; left move = add.
  const reversed: TokenDiff[] = [];
  let i = la;
  let j = lb;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && aa[i - 1] === bb[j - 1]) {
      reversed.push({ type: 'same', text: aa[i - 1] });
      i--;
      j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      reversed.push({ type: 'add', text: bb[j - 1] });
      j--;
    } else {
      reversed.push({ type: 'del', text: aa[i - 1] });
      i--;
    }
  }
  reversed.reverse();
  // Merge consecutive same-type runs so the render is compact (one span per
  // run instead of one per char).
  return mergeRuns(reversed);
}

/** Merge adjacent TokenDiff entries of the same type into one. */
function mergeRuns(toks: TokenDiff[]): TokenDiff[] {
  if (toks.length === 0) return [];
  const out: TokenDiff[] = [{ ...toks[0] }];
  for (let k = 1; k < toks.length; k++) {
    const last = out[out.length - 1];
    if (last.type === toks[k].type) {
      last.text += toks[k].text;
    } else {
      out.push({ ...toks[k] });
    }
  }
  return out;
}
