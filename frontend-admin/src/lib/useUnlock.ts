import { useQuery } from '@tanstack/react-query';
import { api } from './api';

// useUnlockTemplate — fetches the course-level default unlock strategy.
export function useUnlockTemplate(courseId: number | undefined) {
  return useQuery({
    queryKey: ['unlock-template', courseId],
    queryFn: () => api.getUnlockTemplate(courseId!),
    enabled: courseId != null,
    staleTime: 30_000,
  });
}

// useUnlockPreview — the resolved "this student currently sees X/Y episodes"
// readout for one (user, course).
export function useUnlockPreview(userId: number | undefined, courseId: number | undefined) {
  return useQuery({
    queryKey: ['unlock-preview', userId, courseId],
    queryFn: () => api.unlockPreview(userId!, courseId!),
    enabled: userId != null && courseId != null,
    staleTime: 5_000,
  });
}

// Strategy metadata for rendering selectors and labels in one place.
export const STRATEGIES: { key: string; label: string; hint: string }[] = [
  { key: 'all_open', label: '全部开放', hint: '所有课时立即可见（默认）' },
  { key: 'manual', label: '手动解锁', hint: '初始可见第 1 节，admin 每点一次「手动解锁」推进一节' },
  { key: 'interval', label: '固定间隔', hint: '从分配时刻起，每隔 N 天自动解锁一节' },
  { key: 'weekly', label: '每周定时', hint: '在每周的指定时间点（如周日 19:00）自动解锁一节' },
  { key: 'selected', label: '自选课时', hint: '完全由 admin 勾选可见课时，可跳选；无自动推进' },
];

export const WEEKDAY_LABELS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

export function strategyLabel(key: string): string {
  return STRATEGIES.find((s) => s.key === key)?.label ?? key;
}
