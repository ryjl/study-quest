import { describe, it, expect } from 'vitest';
import { scopeLabel } from './ScopeRow';

// scopeLabel maps the advice-scope discriminator to the Chinese noun used in
// toast + confirm copy ("已入队 课程建议", "删除该学生的 学科建议?"). Pinning the
// exact strings guards against accidentally changing user-visible wording.

describe('scopeLabel', () => {
  it('returns 课时 for episode', () => {
    expect(scopeLabel('episode')).toBe('课时');
  });

  it('returns 课程 for course', () => {
    expect(scopeLabel('course')).toBe('课程');
  });

  it('returns 学科 for subject', () => {
    expect(scopeLabel('subject')).toBe('学科');
  });
});
