import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './api';

// GradeTagEntry 是 /admin/api/courses/grade-tags 返回的单个 tag 条目。
//   - preset=true: 5 个内置 tag(primary/junior/senior/adult/universal),label 已本地化。
//   - preset=false: admin 加过的自定义 tag(或 legacy "1"-"9" 数字),label 原样。
export interface GradeTagEntry {
  key: string;
  label: string;
  preset: boolean;
}

// useGradeTags —— 拉 DB 实际可用的 grade tag 列表(预设 + 已用的自定义),
// 给 admin 过滤栏动态渲染用。staleTime 60s:课程列表 invalidate 时也会刷新。
export function useGradeTags() {
  return useQuery<GradeTagEntry[]>({
    queryKey: ['grade-tags'],
    queryFn: () => api.listGradeTags(),
    staleTime: 60_000,
  });
}

// 让 mutation 在创建/更新/删除课程后刷新 tag 列表(可能新增了自定义 tag)。
export function useInvalidateGradeTags() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ['grade-tags'] });
}
