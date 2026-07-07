import { describe, it, expect } from 'vitest';
import {
  formatDuration,
  formatDurationShort,
  formatFileSize,
  resolutionLabel,
  codecLabel,
  parseMediaMeta,
  parseAttachmentJSON,
} from './format';
import { gradeDisplay } from './types';

describe('formatDuration', () => {
  it('returns --:-- for falsy values', () => {
    expect(formatDuration(null)).toBe('--:--');
    expect(formatDuration(undefined)).toBe('--:--');
    expect(formatDuration(0)).toBe('--:--');
    expect(formatDuration(-5)).toBe('--:--');
  });

  it('formats seconds as m:ss', () => {
    expect(formatDuration(65)).toBe('1:05');
    expect(formatDuration(599)).toBe('9:59');
  });

  it('formats hours as h:mm:ss', () => {
    expect(formatDuration(3661)).toBe('1:01:01');
    expect(formatDuration(7384)).toBe('2:03:04');
  });
});

describe('formatDurationShort', () => {
  it('returns 0分 for falsy', () => {
    expect(formatDurationShort(null)).toBe('0分');
    expect(formatDurationShort(0)).toBe('0分');
  });

  it('formats minutes only when < 1h', () => {
    expect(formatDurationShort(60)).toBe('1分');
    expect(formatDurationShort(599)).toBe('9分');
  });

  it('formats hours+minutes when >= 1h', () => {
    expect(formatDurationShort(3600)).toBe('1时0分');
    expect(formatDurationShort(3661)).toBe('1时1分');
    expect(formatDurationShort(7384)).toBe('2时3分');
  });
});

describe('formatFileSize', () => {
  it('returns empty for falsy', () => {
    expect(formatFileSize(null)).toBe('');
    expect(formatFileSize(0)).toBe('');
  });

  it('formats bytes without decimal', () => {
    expect(formatFileSize(512)).toBe('512 B');
  });

  it('formats KB/MB/GB with one decimal', () => {
    expect(formatFileSize(1024)).toBe('1.0 KB');
    expect(formatFileSize(1048576)).toBe('1.0 MB');
    expect(formatFileSize(1572864)).toBe('1.5 MB');
    expect(formatFileSize(1073741824)).toBe('1.0 GB');
  });
});

describe('resolutionLabel', () => {
  it('returns empty when missing', () => {
    expect(resolutionLabel()).toBe('');
    expect(resolutionLabel(0, 0)).toBe('');
  });

  it('maps common heights to labels', () => {
    expect(resolutionLabel(1920, 1080)).toBe('1080p');
    expect(resolutionLabel(1280, 720)).toBe('720p');
    expect(resolutionLabel(3840, 2160)).toBe('4K');
    expect(resolutionLabel(2560, 1440)).toBe('2K');
  });

  it('falls back to WxH for unusual sizes', () => {
    expect(resolutionLabel(800, 600)).toBe('800×600');
  });
});

describe('codecLabel', () => {
  it('returns empty for undefined', () => {
    expect(codecLabel(undefined)).toBe('');
  });

  it('maps known codecs', () => {
    expect(codecLabel('h264')).toBe('H.264');
    expect(codecLabel('hevc')).toBe('H.265');
    expect(codecLabel('H264')).toBe('H.264'); // case-insensitive
  });

  it('uppercases unknown codecs', () => {
    expect(codecLabel('vp9')).toBe('VP9');
  });
});

describe('gradeDisplay', () => {
  it('handles universal', () => {
    expect(gradeDisplay('universal')).toBe('全学段通用');
  });

  it('handles single grade', () => {
    expect(gradeDisplay('3')).toBe('3年级');
  });

  it('handles comma-separated with universal', () => {
    expect(gradeDisplay('3,universal')).toBe('3年级, 通用');
  });

  it('handles empty', () => {
    expect(gradeDisplay('')).toBe('');
  });
});

describe('parseMediaMeta', () => {
  it('returns null for empty', () => {
    expect(parseMediaMeta(undefined)).toBeNull();
    expect(parseMediaMeta('')).toBeNull();
  });

  it('parses valid JSON', () => {
    expect(parseMediaMeta('{"duration_seconds":100,"width":1280}')).toEqual({ duration_seconds: 100, width: 1280 });
  });

  it('returns null for invalid JSON', () => {
    expect(parseMediaMeta('not json')).toBeNull();
  });
});

describe('parseAttachmentJSON', () => {
  it('returns empty array for falsy', () => {
    expect(parseAttachmentJSON(undefined)).toEqual([]);
    expect(parseAttachmentJSON('')).toEqual([]);
  });

  it('parses array of paths', () => {
    expect(parseAttachmentJSON('["/a.pdf","/b.docx"]')).toEqual(['/a.pdf', '/b.docx']);
  });

  it('returns empty for non-array', () => {
    expect(parseAttachmentJSON('{}')).toEqual([]);
    expect(parseAttachmentJSON('"string"')).toEqual([]);
  });

  it('returns empty for invalid JSON', () => {
    expect(parseAttachmentJSON('{bad')).toEqual([]);
  });
});
