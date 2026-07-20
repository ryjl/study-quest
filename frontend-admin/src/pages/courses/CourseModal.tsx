import { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { api } from '../../lib/api';
import { type Course } from '../../lib/types';
import { useSubjects } from '../../lib/useSubjects';
import { Modal } from '../../components/ui';
import { GradePicker } from '../../components/inputs';
import { TagInput } from '../../components/TagInput';
import { ImageUpload } from '../../components/inputs';
import { useToast } from '../../lib/toast';
import { BookOpen, Film } from 'lucide-react';

export function CreateEditCourseModal({
  open,
  course,
  onClose,
  onSaved,
}: {
  open: boolean;
  course?: Course;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = !!course;
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const subjectsQ = useSubjects();
  const subjects = subjectsQ.data ?? [];

  const [title, setTitle] = useState('');
  const [grade, setGrade] = useState('');
  const [subject, setSubject] = useState('');
  const [contentType, setContentType] = useState<'learning' | 'entertainment'>('learning');
  const [coverUrl, setCoverUrl] = useState('');
  const [tagIDs, setTagIDs] = useState<number[]>([]);
  // AI 提示配置(5 字段 hint)已迁移到 AI 控制台的 Prompt 配置 tab。这里只保留
  // 课程级 AI 开关 —— "启用 AI 总结/出题"是课程本体的属性(决定该课程参不参与 AI 后处理),
  // 和具体的 prompt hint(风格性、调优频繁)分离开。save 时 ai_config 原值回传,不会被本表单误改。
  const [aiSummaryEnabled, setAiSummaryEnabled] = useState(true);
  const [aiQuizEnabled, setAiQuizEnabled] = useState(true);

  // Resync ALL form fields only when the modal (re)opens or switches to a
  // different course. We intentionally do NOT depend on `subjects` here: the
  // subject catalog loads asynchronously, and a mid-edit catalog arrival used
  // to reset a subject the admin had just picked (the effect re-ran on every
  // subjects change, re-seeding subject from subjects[0]). Instead, the
  // subject fallback to subjects[0] is applied ONCE at open via the inline
  // read of the current `subjects` prop; later catalog updates don't disturb
  // the form. The <select> below reads `subjects` reactively for its options,
  // so newly-arrived subjects still appear in the dropdown.
  useEffect(() => {
    if (open) {
      setTitle(course?.title ?? '');
      // Backend DTO sends `grades` (plural, see admin_dto.go); the legacy
      // `grade` singular alias may still appear on older rows. Fall back so
      // editing an existing course preserves its grade selection instead of
      // silently clearing it (the prior bug: reading only `grade` always got
      // "" because the backend stopped sending that field).
      setGrade(course?.grades ?? course?.grade ?? '');
      const ct = (course?.content_type === 'entertainment' ? 'entertainment' : 'learning') as 'learning' | 'entertainment';
      setContentType(ct);
      // 2026-07-20:不再强制 entertainment 课程用 'entertainment' 占位 subject。
      // 直接保留 course.subject(可能是 animation/movie 等娱乐子类 key)。
      // 新建时若是娱乐类型,默认选第一个娱乐子类(避免空 subject)。
      if (course?.subject) {
        setSubject(course.subject);
      } else if (ct === 'entertainment') {
        const firstEnt = subjects.find((s) => s.category === 'entertainment');
        setSubject(firstEnt?.key ?? subjects[0]?.key ?? '');
      } else {
        const firstAcad = subjects.find((s) => s.category === 'academic' || !s.category);
        setSubject(firstAcad?.key ?? subjects[0]?.key ?? '');
      }
      setCoverUrl(course?.cover_url ?? '');
      setTagIDs(course?.tag_ids ?? []);
      // AI switches default OFF when unset — AI is an opt-in add-on layer; a
      // course with no explicit setting behaves as plain video viewing (no AI
      // surfaces). Matches the backend gorm:"default:false".
      setAiSummaryEnabled(course?.ai_summary_enabled ?? false);
      setAiQuizEnabled(course?.ai_quiz_enabled ?? false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, course]);

  const isEntertainment = contentType === 'entertainment';

  const saveMut = useMutation({
    mutationFn: async () => {
      if (!title.trim()) throw new Error('请输入课程名称');
      const grades = grade.split(',').map((g) => g.trim()).filter(Boolean);
      if (grades.length === 0) throw new Error('请至少选择一个适用年级');
      const body = {
        title: title.trim(),
        // Send BOTH the new `grades` field (what the backend reads — see
        // admin_content.go parseGrades) and the legacy `grade` alias for
        // backward compatibility with any older middleware path.
        grades: grade,
        grade,
        // 2026-07-20:subject 和 content_type 不再硬绑定。直接传当前选中的 subject
        // (可能是 academic 或 entertainment 子类的 key)。后端 import_service 会
        // 根据 subject.Category 自动判定 content_type,这里仍然显式传 content_type
        // 保持一致性。
        subject,
        content_type: contentType,
        cover_url: coverUrl,
        tag_ids: tagIDs,
        // ai_config(5 字段 hint)原值回传 —— 本表单不再编辑 hint(已挪到 AI 控制台
        // 的 Prompt 配置 tab)。回传 course.ai_config 保证 PUT 不把这 5 字段误清。
        // 新建课程时 course 为 undefined,ai_config 为 undefined,后端会建空配置。
        ai_config: course?.ai_config,
        ai_summary_enabled: aiSummaryEnabled,
        ai_quiz_enabled: aiQuizEnabled,
      };
      if (isEdit && course) return api.updateCourse(course.id, body);
      return api.createCourse(body);
    },
    onSuccess: () => {
      toast.success(isEdit ? '课程已更新' : '课程已创建');
      qc.invalidateQueries({ queryKey: ['courses'] });
      onSaved();
    },
    onError: (e) => toast.error((e as Error).message),
  });

  return (
    <Modal open={open} onClose={onClose} title={isEdit ? '编辑课程' : '新增课程库'} size="md">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          saveMut.mutate();
        }}
        className="space-y-4"
      >
        <div>
          <label className="mb-1 block text-xs text-muted">课程名称</label>
          <input className="input" placeholder="如：神奇的物理世界" value={title} onChange={(e) => setTitle(e.target.value)} required autoFocus />
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted">适用年级（可多选）</label>
          <GradePicker value={grade} onChange={setGrade} />
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted">内容类型</label>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => {
                setContentType('learning');
                // 切到学习时,若当前 subject 是娱乐子类,清空让用户重选学术科目。
                const cur = subjects.find((s) => s.key === subject);
                if (cur?.category === 'entertainment') {
                  const firstAcad = subjects.find((s) => s.category === 'academic' || !s.category);
                  setSubject(firstAcad?.key ?? '');
                }
              }}
              className={`flex flex-1 items-center justify-center gap-1.5 rounded-md border px-3 py-2 text-sm transition-colors ${!isEntertainment ? 'border-txt bg-card-2 text-txt font-medium' : 'border-border text-muted hover:text-txt'}`}
            >
              <BookOpen size={14} /> 学习
            </button>
            <button
              type="button"
              onClick={() => {
                setContentType('entertainment');
                // 切到娱乐时,若当前 subject 是学术科目,清空让用户重选娱乐子类。
                const cur = subjects.find((s) => s.key === subject);
                if (!cur || cur.category === 'academic' || !cur.category) {
                  const firstEnt = subjects.find((s) => s.category === 'entertainment');
                  setSubject(firstEnt?.key ?? '');
                }
              }}
              className={`flex flex-1 items-center justify-center gap-1.5 rounded-md border px-3 py-2 text-sm transition-colors ${isEntertainment ? 'border-txt bg-card-2 text-txt font-medium' : 'border-border text-muted hover:text-txt'}`}
            >
              <Film size={14} /> 娱乐
            </button>
          </div>
        </div>

        {/* 科目下拉:根据 content type 过滤显示对应 category 的 subject。
            2026-07-20:不再隐藏整个块,娱乐课也能选科目(动画片/电影/纪录片/综艺)。 */}
        <div>
          <label className="mb-1 block text-xs text-muted">
            {isEntertainment ? '娱乐分类' : '类别 / 科目'}
          </label>
          <select className="input" value={subject} onChange={(e) => setSubject(e.target.value)}>
            {subjects
              .filter((s) => {
                // 学习课:显示 academic(或没标 category 的旧数据,默认按 academic 处理)。
                // 娱乐课:显示 entertainment。
                if (isEntertainment) return s.category === 'entertainment';
                return s.category === 'academic' || !s.category;
              })
              .map((s) => (
                <option key={s.key} value={s.key}>
                  {s.label} ({s.key})
                </option>
              ))}
          </select>
        </div>

        <ImageUpload label="封面图" value={coverUrl} onChange={setCoverUrl} />

        <div>
          <label className="mb-1 block text-xs text-muted">标签</label>
          <TagInput value={tagIDs} onChange={setTagIDs} />
        </div>

        {/* AI 提示配置已迁移到「AI 控制台 → Prompt 配置」tab,这里只留跳转入口。
            2026-07-20:娱乐课也支持 AI(字幕→summary→quiz→advice 链已放开),
            所以 AI 配置入口不再隐藏。课程基本信息(title/grade/subject/cover/tags)
            和课程级 AI 开关仍在这里;具体的 5 字段 hint 配置挪到 AI 控制台集中管理。 */}
        <div className="flex items-center justify-between rounded-xl border border-border bg-card-2 p-3">
          <div>
            <div className="text-xs font-medium text-txt">AI 提示与 Prompt 预览</div>
            <div className="mt-0.5 text-[11px] text-muted">5 个 hint(Whisper/总结/出题/建议/术语字典)+ 学科默认 + Prompt 预览,集中管理。</div>
          </div>
          <button
            type="button"
            onClick={() => {
              if (!isEdit || !course) {
                toast.info('请先创建课程,再配置 AI 提示');
                return;
              }
              navigate(`/admin/ai-console?tab=prompt&course=${course.id}`);
            }}
            className="flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-[11px] text-muted transition-colors hover:border-primary hover:text-primary"
            title="跳转到 AI 控制台 配置该课程的 AI 提示"
          >
            配置 →
          </button>
        </div>

        <div className="space-y-2 rounded-xl border border-border bg-card-2 p-3">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              className="h-4 w-4 rounded border-border accent-primary"
              checked={aiSummaryEnabled}
              onChange={(e) => setAiSummaryEnabled(e.target.checked)}
            />
            <span>启用 AI 总结</span>
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              className="h-4 w-4 rounded border-border accent-primary"
              checked={aiQuizEnabled}
              onChange={(e) => setAiQuizEnabled(e.target.checked)}
            />
            <span>启用 AI 出题</span>
          </label>
          <p className="text-[11px] text-muted">关闭后，该课程的课时将跳过对应的 AI 后处理（即使全局已配置）。AI 提示内容请到「AI 控制台 → Prompt 配置」设置。</p>
        </div>

        <button type="submit" className="btn-primary w-full" disabled={saveMut.isPending}>
          {saveMut.isPending ? '保存中...' : isEdit ? '保存修改' : '保存并创建'}
        </button>
      </form>
    </Modal>
  );
}
