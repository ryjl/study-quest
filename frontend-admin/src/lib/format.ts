// Formatting helpers used across the admin UI.

export function formatDuration(seconds: number | null | undefined): string {
  if (!seconds || seconds <= 0) return '--:--';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  return `${m}:${String(s).padStart(2, '0')}`;
}

export function formatDurationShort(seconds: number | null | undefined): string {
  // Compact form for badges: "12分" or "1时30分"
  if (!seconds || seconds <= 0) return '0分';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h > 0) return `${h}时${m}分`;
  return `${m}分`;
}

export function formatFileSize(bytes: number | null | undefined): string {
  if (!bytes || bytes <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let n = bytes;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

export function resolutionLabel(width?: number, height?: number): string {
  if (!width || !height) return '';
  // Common labels: 480p/720p/1080p/1440p/2160p by height
  const map: Record<number, string> = {
    2160: '4K',
    1440: '2K',
    1080: '1080p',
    720: '720p',
    480: '480p',
    360: '360p',
  };
  return map[height] ?? `${width}×${height}`;
}

export function codecLabel(codec?: string): string {
  if (!codec) return '';
  const map: Record<string, string> = {
    h264: 'H.264',
    hevc: 'H.265',
    av1: 'AV1',
    vp9: 'VP9',
    aac: 'AAC',
    mp3: 'MP3',
    ac3: 'AC3',
    flac: 'FLAC',
    opus: 'Opus',
  };
  return map[codec.toLowerCase()] ?? codec.toUpperCase();
}

export function formatDate(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  if (sameDay) {
    return `今天 ${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
  }
  const yest = new Date(now);
  yest.setDate(now.getDate() - 1);
  if (d.toDateString() === yest.toDateString()) {
    return `昨天 ${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
  }
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).toString().padStart(2, '0')}`;
}

export function relativeTime(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const diff = Date.now() - d.getTime();
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return '刚刚';
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day} 天前`;
  return formatDate(iso);
}

export function parseMediaMeta(json?: string): { duration_seconds?: number; width?: number; height?: number; video_codec?: string; audio_codec?: string } | null {
  if (!json) return null;
  try {
    return JSON.parse(json);
  } catch {
    return null;
  }
}

export function parseAttachmentJSON(json?: string): string[] {
  if (!json) return [];
  try {
    const v = JSON.parse(json);
    return Array.isArray(v) ? v : [];
  } catch {
    return [];
  }
}

// Format an ISO timestamp using zh-CN locale (24h). Returns '—' for falsy or
// unparseable input. Shared by SubtitleQueue and AIWorkflow.
export function fmtTime(s?: string | null): string {
  if (!s) return '—';
  try {
    return new Date(s).toLocaleString('zh-CN', { hour12: false });
  } catch {
    return s;
  }
}

// Role key → Chinese label for activity feeds and user tables.
export function roleLabel(role: string): string {
  switch (role) {
    case 'student':
      return '学生';
    case 'teen':
      return '青少年';
    case 'parent':
      return '家长';
    case 'admin':
      return '管理员';
    default:
      return role;
  }
}

// Compact watch-time formatter. Uses raw seconds for sub-minute precision so
// a user who watched e.g. 40 seconds doesn't show a misleading "0 分".
export function formatWatchTime(seconds?: number): string {
  if (seconds !== undefined && seconds > 0) {
    const s = Math.floor(seconds);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const rem = s % 60;
    if (h > 0) return rem === 0 ? (m === 0 ? `${h} 时` : `${h} 时 ${m} 分`) : `${h} 时 ${m} 分`;
    if (m > 0) return rem === 0 ? `${m} 分` : `${m} 分 ${rem} 秒`;
    return `${rem} 秒`;
  }
  return '0 分';
}

// m:ss formatter for short clip/segment durations.
export function fmtSec(s: number): string {
  const m = Math.floor(s / 60);
  const sec = s % 60;
  return `${m}:${sec.toString().padStart(2, '0')}`;
}
