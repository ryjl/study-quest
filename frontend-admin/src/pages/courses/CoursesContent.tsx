import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../../lib/api';
import { subjectMeta, type Course } from '../../lib/types';
import { useSubjects } from '../../lib/useSubjects';
import { useGradeTags } from '../../lib/useGradeTags';
import { useUnlockTemplate, strategyLabel } from '../../lib/useUnlock';
import { EmptyState, LoadingState, Modal, SubjectBadge, SubjectIcon, Tag } from '../../components/ui';
import { CourseUnlockTemplateModal } from '../../components/CourseUnlockTemplateModal';
import { formatDurationShort, relativeTime } from '../../lib/format';
import { useToast, useConfirm } from '../../lib/toast';
import { sortBy, timeValue, type SortDir, type SortOption } from '../../lib/sort';
import { CourseTree } from './CourseTree';
import {
  Search,
  ChevronRight,
  Clock,
  Pencil,
  Trash2,
  ArrowUp,
  ArrowDown,
  Library,
  Eye,
} from 'lucide-react';

// Display-sort options for the course list. Pure display — does NOT rewrite
// sort_order; "apply as order" persistence is out of scope here (the manual
// ▲/▼ controls on episodes are the persistent path).
const COURSE_SORT_OPTIONS: SortOption<Course>[] = [
  { key: 'updated', label: '更新时间', value: (c) => timeValue(c.updated_at) },
  { key: 'created', label: '创建时间', value: (c) => timeValue(c.created_at) },
  { key: 'title', label: '标题', value: (c) => c.title },
  { key: 'duration', label: '总时长', value: (c) => c.total_duration_seconds ?? 0 },
  { key: 'episodes', label: '课时数', value: (c) => c.episode_count ?? 0 },
  { key: 'subject', label: '科目', value: (c) => c.subject },
];

export function CoursesContent({ onEdit, onChanged }: { onEdit: (c: Course) => void; onChanged: () => void }) {
  const [search, setSearch] = useState('');
  const [gradeFilter, setGradeFilter] = useState('all');
  const [subjectFilter, setSubjectFilter] = useState('all');
  const [sortKey, setSortKey] = useState('updated');
  const [sortDir, setSortDir] = useState<SortDir>('desc');
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [unlockForCourse, setUnlockForCourse] = useState<Course | null>(null);
  // previewForCourse 是当前打开"预览 AI Prompt"Modal 的课程。null=关。Modal 里选 agent
  // (summary/quiz/advice),调预览端点展示拼好的完整 prompt(不调 LLM,纯文本)。
  const [previewForCourse, setPreviewForCourse] = useState<Course | null>(null);
  const qc = useQueryClient();
  const toast = useToast();
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];
  // gradeTags 动态拉取(预设 + admin 已用的自定义 tag),替代旧的硬编码 GRADES。
  const gradeTagsQ = useGradeTags();
  const gradeTags = gradeTagsQ.data ?? [];
  const confirm = useConfirm();

  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const courses = coursesQ.data ?? [];

  const filtered = useMemo(() => {
    const f = courses.filter((c) => {
      if (gradeFilter !== 'all') {
        const grades = (c.grade || '').split(',').map((g) => g.trim());
        if (!grades.includes(gradeFilter)) return false;
      }
      if (subjectFilter !== 'all' && c.subject !== subjectFilter) return false;
      if (search.trim()) {
        const q = search.toLowerCase();
        const hay = `${c.title} ${c.subject} ${c.tags_list?.join(' ') ?? ''}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      return true;
    });
    const opt = COURSE_SORT_OPTIONS.find((o) => o.key === sortKey) ?? COURSE_SORT_OPTIONS[0];
    return sortBy(f, opt, sortDir);
  }, [courses, gradeFilter, subjectFilter, search, sortKey, sortDir]);

  const deleteCourseMut = useMutation({
    mutationFn: (id: number) => api.deleteCourse(id),
    onSuccess: () => {
      toast.success('课程已删除');
      qc.invalidateQueries({ queryKey: ['courses'] });
      onChanged();
    },
    onError: (e) => toast.error('删除失败: ' + (e as Error).message),
  });

  const toggleExpand = (id: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const onDeleteCourse = async (c: Course) => {
    const ok = await confirm({
      message: `删除课程「${c.title}」？`,
      detail: '此操作将连带删除旗下所有章节、课时、进度和字幕记录，且不可撤销。',
      danger: true,
    });
    if (ok) deleteCourseMut.mutate(c.id);
  };

  if (coursesQ.isLoading) return <LoadingState />;
  if (coursesQ.error) return <div className="card text-bad">加载失败: {(coursesQ.error as Error).message}</div>;

  if (courses.length === 0) {
    return <EmptyState icon={<Library size={32} />} title="课程库为空" hint="点击右上角「新增课程库」创建您的第一个课程。" />;
  }

  return (
    <div>
      {/* Filter toolbar — two rows. Row 1: search (primary) + count.
          Row 2: filters + sort (secondary), wraps on narrow viewports.
          Replaces the old single cramped row that jammed 5+ controls together. */}
      <div className="mb-4 rounded-lg border border-border/60 bg-card px-3 py-2.5">
        {/* Row 1 */}
        <div className="flex items-center gap-3">
          <div className="relative max-w-md flex-1">
            <Search size={14} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-muted" />
            <input
              className="input !pl-9"
              placeholder="搜索课程名 / 标签..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <span className="ml-auto whitespace-nowrap text-xs tabular-nums text-muted">
            <span className="font-medium text-txt">{filtered.length}</span> / {courses.length} 门
          </span>
        </div>
        {/* Row 2 */}
        <div className="mt-2.5 flex flex-wrap items-center gap-2 border-t border-border/60 pt-2.5">
          {/* Grade filter chips —— 动态拉取,含自定义 tag */}
          <div className="flex flex-wrap items-center gap-1">
            <FilterChip active={gradeFilter === 'all'} onClick={() => setGradeFilter('all')}>
              全部学段
            </FilterChip>
            {gradeTags.map((g) => (
              <FilterChip key={g.key} active={gradeFilter === g.key} onClick={() => setGradeFilter(g.key)}>
                {g.label}
              </FilterChip>
            ))}
          </div>
          <div className="mx-1 h-4 w-px bg-border" />
          {/* Subject filter */}
          <select
            className="input !w-auto !py-1 !text-xs"
            value={subjectFilter}
            onChange={(e) => setSubjectFilter(e.target.value)}
          >
            <option value="all">全部科目</option>
            {subjects.map((s) => (
              <option key={s.key} value={s.key}>
                {s.label}
              </option>
            ))}
          </select>
          {/* Sort — pushed to the right */}
          <div className="ml-auto flex items-center gap-2">
            <select
              className="input !w-auto !py-1 !text-xs"
              value={sortKey}
              onChange={(e) => setSortKey(e.target.value)}
            >
              {COURSE_SORT_OPTIONS.map((o) => (
                <option key={o.key} value={o.key}>
                  {o.label}
                </option>
              ))}
            </select>
            <button
              className="btn-ghost btn-sm"
              onClick={() => setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))}
              title={sortDir === 'asc' ? '当前：正序（点击切换为倒序）' : '当前：倒序（点击切换为正序）'}
            >
              {sortDir === 'asc' ? <ArrowUp size={14} /> : <ArrowDown size={14} />}
            </button>
          </div>
        </div>
      </div>

      {filtered.length === 0 ? (
        <EmptyState icon={<Search size={32} />} title="无匹配课程" hint="尝试调整搜索或筛选条件" />
      ) : (
        <div className="flex flex-col gap-2 pb-8">
          {filtered.map((c) => {
            const isOpen = expanded.has(c.id);
            // Effective cover: explicit cover → backend-derived first-episode
            // fallback → styled CSS placeholder (subject gradient + first char).
            // The placeholder is generated in-browser so there's no font/storage
            // cost; it just looks better than a bare emoji. Resolve the subject
            // meta from the reactive subjects list (not the module cache) so the
            // first-paint-before-catalog-loads race doesn't render the raw key +
            // grey fallback here either.
            const meta = subjects.find((x) => x.key === c.subject) ?? subjectMeta(c.subject);
            const cover = c.cover_url || c.cover_fallback_url || '';
            return (
              <div key={c.id} className="overflow-hidden rounded-lg border border-border/60 bg-card">
                {/* Card header — the whole row is the expand affordance. The
                    right-side ⋯ menu lives in a stopPropagation wrapper so its
                    clicks don't bubble up and toggle the card. */}
                <div
                  className="group flex items-center gap-3 px-4 py-3 transition-colors hover:bg-card-2/40"
                  onClick={() => toggleExpand(c.id)}
                >
                  {/* Expand indicator — small chevron at the left, rotates open.
                      Replaces the old huge violet ▶ button. */}
                  <ChevronRight
                    size={16}
                    className={`flex-shrink-0 text-muted transition-transform duration-150 ${isOpen ? 'rotate-90' : ''}`}
                  />

                  {/* Cover thumbnail — h-16 w-16, a touch smaller than the old h-20 */}
                  <div className="h-16 w-16 flex-shrink-0 overflow-hidden rounded-md border border-border bg-card-2">
                    {cover ? (
                      <img src={cover} alt="" className="h-full w-full object-cover" onError={(e) => ((e.target as HTMLImageElement).style.display = 'none')} />
                    ) : (
                      <div
                        className="flex h-full w-full items-center justify-center"
                        style={{ background: `linear-gradient(135deg, ${meta.color}33, ${meta.color}0d)` }}
                      >
                        <SubjectIcon subject={c.subject} size={22} />
                      </div>
                    )}
                  </div>

                  <div className="min-w-0 flex-1">
                    {/* Primary: title */}
                    <h3 className="truncate text-base font-semibold text-txt">{c.title}</h3>
                    {/* Secondary: meta line — subject badge + grade + counts +
                        duration + updated, dot-separated, all muted. Replaces
                        the old 6+ individually-styled spans. */}
                    <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
                      <SubjectBadge subject={c.subject} />
                      <CourseUnlockBadge courseId={c.id} />
                      {c.grade_display && (
                        <>
                          <Sep />
                          <span>{c.grade_display}</span>
                        </>
                      )}
                      <Sep />
                      <span className="tabular-nums">{c.chapter_count ?? 0} 章</span>
                      <Sep />
                      <span className="tabular-nums">{c.episode_count ?? 0} 课时</span>
                      {c.total_duration_seconds ? (
                        <>
                          <Sep />
                          <span className="whitespace-nowrap tabular-nums">{formatDurationShort(c.total_duration_seconds)}</span>
                        </>
                      ) : null}
                      {c.updated_at && (
                        <>
                          <Sep />
                          <span>更新 {relativeTime(c.updated_at)}</span>
                        </>
                      )}
                    </div>
                    {c.tags_list && c.tags_list.length > 0 && (
                      <div className="mt-1.5 flex flex-wrap gap-1">
                        {c.tags_list.map((t) => (
                          <Tag key={t}>{t}</Tag>
                        ))}
                      </div>
                    )}
                  </div>

                  {/* 操作按钮组:直接露出图标按钮,不用三点菜单(展开/折叠已由
                      左侧 chevron + 点卡片头部承担,不再冗余露出)。stopPropagation
                      让点按钮不触发卡片展开。风格对齐 CourseTree 的章节/课时行按钮。 */}
                  <div className="flex flex-shrink-0 items-center gap-0.5" onClick={(e) => e.stopPropagation()}>
                    <button className="btn-ghost btn-sm !px-1.5" onClick={() => setUnlockForCourse(c)} title="解锁节奏">
                      <Clock size={14} />
                    </button>
                    <button className="btn-ghost btn-sm !px-1.5" onClick={() => setPreviewForCourse(c)} title="预览 AI Prompt">
                      <Eye size={14} />
                    </button>
                    <button className="btn-ghost btn-sm !px-1.5" onClick={() => onEdit(c)} title="编辑课程">
                      <Pencil size={14} />
                    </button>
                    <button className="btn-ghost btn-sm !px-1.5 text-bad hover:text-bad" onClick={() => onDeleteCourse(c)} title="删除课程">
                      <Trash2 size={14} />
                    </button>
                  </div>
                </div>

                {/* CourseTree brings its own border-t + bg-card + p-4, so render
                    it directly under the header with no extra wrapper/separator. */}
                {isOpen && <CourseTree course={c} onChanged={onChanged} />}
              </div>
            );
          })}
        </div>
      )}

      {unlockForCourse && (
        <CourseUnlockTemplateModal
          courseId={unlockForCourse.id}
          courseTitle={unlockForCourse.title}
          onClose={() => setUnlockForCourse(null)}
          onSaved={() => qc.invalidateQueries({ queryKey: ['unlock-template'] })}
        />
      )}

      {previewForCourse && (
        <PreviewPromptModal course={previewForCourse} onClose={() => setPreviewForCourse(null)} />
      )}
    </div>
  );
}

// PreviewPromptModal:选一个 agent(summary/quiz/advice),调预览端点展示该课程最终会
// 拼出的完整 system+user prompt(不调 LLM,纯文本)。admin 调优 hint 后立刻看效果。
// agent 切换时自动重新拉取(query key 含 agent)。resolved_hints 展示"现在生效的是哪个值"。
type PreviewAgent = 'summary' | 'quiz' | 'advice';
const PREVIEW_AGENTS: { key: PreviewAgent; label: string; desc: string }[] = [
  { key: 'summary', label: '总结', desc: 'summary agent' },
  { key: 'quiz', label: '出题', desc: 'quiz agent' },
  { key: 'advice', label: '建议', desc: 'advice agent' },
];

function PreviewPromptModal({ course, onClose }: { course: Course; onClose: () => void }) {
  const [agent, setAgent] = useState<PreviewAgent>('summary');
  const q = useQuery({
    queryKey: ['preview-prompt', course.id, agent],
    queryFn: () => api.previewCoursePrompt(course.id, agent),
    retry: false,
  });
  const err = q.error as Error | undefined;
  return (
    <Modal open onClose={onClose} title={`预览 AI Prompt — ${course.title}`} size="xl">
      <div className="space-y-3 p-5 pt-2">
        {/* Agent 切换 chips */}
        <div className="flex flex-wrap items-center gap-1.5">
          {PREVIEW_AGENTS.map((a) => (
            <button
              key={a.key}
              onClick={() => setAgent(a.key)}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                agent === a.key ? 'bg-txt text-bg' : 'text-muted hover:bg-card-2 hover:text-txt'
              }`}
              title={a.desc}
            >
              {a.label}
            </button>
          ))}
          <span className="ml-auto text-[11px] text-muted">不调 LLM,纯本地 prompt 拼装</span>
        </div>

        {q.isLoading && <div className="py-8 text-center text-sm text-muted">加载中…</div>}
        {err && <div className="rounded-md border border-border bg-card-2 p-3 text-sm text-bad">{err.message}</div>}

        {q.data && (
          <>
            {/* resolved_hints:展示解析结果,让 admin 看到"现在生效的是学科默认还是课程覆盖"。 */}
            <div>
              <div className="mb-1 text-xs font-medium text-muted">解析后的 hints(课程级覆盖学科级)</div>
              <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
                <HintBlock label="WhisperHint" value={q.data.resolved_hints.whisper_hint} />
                <HintBlock label="SummaryHint" value={q.data.resolved_hints.summary_hint} />
                <HintBlock label="QuizHint" value={q.data.resolved_hints.quiz_hint} />
                <HintBlock label="AdviceHint" value={q.data.resolved_hints.advice_hint} />
                <HintBlock label="TermDict" value={q.data.resolved_hints.term_dict} fullWidth />
              </div>
            </div>

            <div>
              <div className="mb-1 text-xs font-medium text-muted">System Prompt(代码常量)</div>
              <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded-lg border border-border bg-card-2 p-3 text-[11px] text-txt">{q.data.system_prompt || '(空)'}</pre>
            </div>
            <div>
              <div className="mb-1 text-xs font-medium text-muted">User Prompt(含注入的 hint/TermDict)</div>
              <pre className="max-h-80 overflow-auto whitespace-pre-wrap rounded-lg border border-border bg-card-2 p-3 text-[11px] text-txt">{q.data.user_prompt || '(空)'}</pre>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}

// HintBlock:resolved hint 的小卡片。空值也显示(让 admin 看清"这个字段当前没配")。
function HintBlock({ label, value, fullWidth }: { label: string; value: string; fullWidth?: boolean }) {
  return (
    <div className={`rounded-md border border-border bg-card-2 p-2 ${fullWidth ? 'sm:col-span-2' : ''}`}>
      <div className="text-[10px] text-muted">{label}</div>
      <div className="mt-0.5 whitespace-pre-wrap break-words text-[11px] text-txt">{value || <span className="text-muted">(空)</span>}</div>
    </div>
  );
}

// Sep — a muted dot between meta items, cleaner than separate "·" spans.
function Sep() {
  return <span className="text-border">·</span>;
}

// CourseUnlockBadge shows the effective unlock cadence as a small pill on the
// course card header (e.g. "每周解锁"). Hidden for all_open / no-template
// (the default backward-compat state) so default courses stay visually clean.
// This is a separate component because each course needs its own query hook —
// you can't call useUnlockTemplate inside the map callback.
function CourseUnlockBadge({ courseId }: { courseId: number }) {
  const tplQ = useUnlockTemplate(courseId);
  const t = tplQ.data;
  if (!t || !t.exists || t.strategy === 'all_open') return null;
  return (
    <span
      className="rounded border border-warn/30 bg-warn/5 px-1.5 py-0.5 text-[10px] font-medium text-warn"
      title="该课程设置了视频按需解锁节奏"
    >
      {strategyLabel(t.strategy)}
    </span>
  );
}

function FilterChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
        active ? 'bg-txt text-bg' : 'text-muted hover:bg-card-2 hover:text-txt'
      }`}
    >
      {children}
    </button>
  );
}
