import { describe, it, expect } from 'vitest';
import { parseWhitelistForDisplay } from './shared';

// parseWhitelistForDisplay turns the stored whitelist_domains string (which the
// backend serializes as a JSON array) into a clean comma-separated value for
// the edit modal's text input. It must tolerate three storage shapes: empty,
// JSON array, or legacy comma-separated plain text.

describe('parseWhitelistForDisplay', () => {
  it('returns empty for a blank string', () => {
    expect(parseWhitelistForDisplay('')).toBe('');
  });

  it('returns empty for the literal empty-array marker "[]"', () => {
    expect(parseWhitelistForDisplay('[]')).toBe('');
  });

  it('joins a JSON array into a comma-separated string', () => {
    expect(parseWhitelistForDisplay(JSON.stringify(['a.com', 'b.com']))).toBe('a.com, b.com');
  });

  it('drops non-string and empty-string entries from the array', () => {
    expect(parseWhitelistForDisplay(JSON.stringify(['a.com', '', 5, null, 'b.com']))).toBe(
      'a.com, b.com',
    );
  });

  it('passes legacy comma-separated text through unchanged when it is not valid JSON', () => {
    const legacy = 'a.com, b.com, c.com';
    expect(parseWhitelistForDisplay(legacy)).toBe(legacy);
  });

  it('passes a single bare domain through unchanged', () => {
    expect(parseWhitelistForDisplay('example.com')).toBe('example.com');
  });
});
