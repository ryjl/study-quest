import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, FileText, Printer, Sparkles, Eye, EyeOff } from 'lucide-react';
import { api } from '../lib/api';
import { PageHeader } from '../components/PageHeader';
import { EmptyState, Spinner, Tag } from '../components/ui';
import { useToast } from '../lib/toast';
import { formatDate } from '../lib/format';
import type {
  Homework,
  HomeworkQuestionType,
  HomeworkView,
  HomeworkViewQuestion,
  HomeworkViewSection,
} from '../lib/types';
import './Homework.css';

// Homework.tsx — 作业卷(Homework)管理页。后端 Stage 1 已完成,本页是 admin 前端:
//   1. 选课程 → 顶部"为本课生成作业"按钮(批量生成,后端幂等去重)
//   2. 左侧作业列表(active/archived),点某行 → 右侧预览
//   3. 右侧卷面预览 + "显示答案" toggle + "打印" 按钮
//
// 数据源:
//   - api.listCourses() 课程选择器
//   - api.listEpisodes(courseId) 把 episode_id → episode 标题(列表行友好显示)
//   - homework.generate/list/get 作业 API
//
// 打印:点"打印" → window.print()。@media print CSS(Homework.css)隐藏 admin 框架,
//   只渲染 .print-area(HomeworkPrintView 组件)。A4 + 15mm 边距,大题不跨页。
//   零新依赖,纯 CSS。田字格/四线三格用 background-image 渐变线画。

// ---------------------------------------------------------------------------
// 题型中文 label(用于答案标注和大题小字)
// ---------------------------------------------------------------------------
const TYPE_LABEL: Record<HomeworkQuestionType, string> = {
  choice: '选择题',
  multi_choice: '多选题',
  fill: '填空题',
  short_answer: '简答题',
  calculation: '计算题',
  copy_word: '抄写题',
  dictation: '默写题',
  translation: '翻译题',
};

// ---------------------------------------------------------------------------
// 安全 parse scoring/options JSON。后端契约里这两个字段都是 JSON string,
// 但容错:parse 失败时回退到安全空值,避免一道题坏掉整页崩。
// ---------------------------------------------------------------------------
function parseOptions(raw: string): string[] {
  if (!raw) return [];
  try {
    const v = JSON.parse(raw);
    return Array.isArray(v) ? v.map(String) : [];
  } catch {
    return [];
  }
}

interface ParsedScoring {
  correct_index?: number;
  correct_indices?: number[];
  accept?: string[];
  reference?: string;
  content?: string;
  times?: number;
}

function parseScoring(raw: string): ParsedScoring {
  if (!raw) return {};
  try {
    return JSON.parse(raw) as ParsedScoring;
  } catch {
    return {};
  }
}

// ---------------------------------------------------------------------------
// renderAnswerText:把 scoring 解析成可读的参考答案文本(用于 showAnswers 标注)。
// ---------------------------------------------------------------------------
function renderAnswerText(type: HomeworkQuestionType, scoring: ParsedScoring): string {
  switch (type) {
    case 'choice':
      return scoring.correct_index != null
        ? `正确项:${String.fromCharCode(65 + scoring.correct_index)}`
        : '';
    case 'multi_choice': {
      if (!scoring.correct_indices?.length) return '';
      const labels = scoring.correct_indices
        .slice()
        .sort((a, b) => a - b)
        .map((i) => String.fromCharCode(65 + i))
        .join('、');
      return `正确项:${labels}`;
    }
    case 'fill':
      return scoring.accept?.length ? `参考答案:${scoring.accept.join(' / ')}` : '';
    case 'short_answer':
    case 'calculation':
    case 'dictation':
    case 'translation':
      return scoring.reference ? `参考答案:${scoring.reference}` : '';
    case 'copy_word':
      return scoring.content ? `抄写内容:${scoring.content}` : '';
    default:
      return '';
  }
}

// ===========================================================================
// 主页面
// ===========================================================================
export function Homework() {
  const toast = useToast();
  const qc = useQueryClient();

  // 课程选择器数据源。listCourses 返回所有课程,前端选。
  const coursesQ = useQuery({ queryKey: ['courses'], queryFn: api.listCourses });
  const [courseId, setCourseId] = useState<number | null>(null);

  // 选中课程后:拉作业列表。queryKey 带 courseId,切课程自动重取。
  const homeworksQ = useQuery({
    queryKey: ['homeworks', courseId],
    queryFn: () => api.homeworkList(courseId as number),
    enabled: courseId != null,
  });

  // 列表行需要 episode_id → episode 标题。一次性拉该课程的 episodes,本地建 map。
  const episodesQ = useQuery({
    queryKey: ['episodes', courseId],
    queryFn: () => api.listEpisodes(courseId as number),
    enabled: courseId != null,
  });
  const episodeTitle = useMemo(() => {
    const map = new Map<number, string>();
    for (const e of episodesQ.data ?? []) map.set(e.id, e.title);
    return (eid: number) => map.get(eid) ?? `课时 #${eid}`;
  }, [episodesQ.data]);

  // 选中的作业 id(预览)。默认选第一个 active。
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const homeworks = homeworksQ.data ?? [];
  // 列表刷新后如果当前 selectedId 不在新列表里(比如刚生成的还没刷),回退到第一个。
  const effectiveSelectedId =
    selectedId != null && homeworks.some((h) => h.id === selectedId)
      ? selectedId
      : homeworks[0]?.id ?? null;

  // 生成作业 mutation。成功后:弹 toast 显示 enqueued 数 + invalidate 列表。
  const genMut = useMutation({
    mutationFn: () => api.homeworkGenerate(courseId as number),
    onSuccess: (data) => {
      toast.success(`已入队 ${data.enqueued} 份作业(已有在途的课时自动跳过)`);
      qc.invalidateQueries({ queryKey: ['homeworks', courseId] });
    },
    onError: (e: unknown) => toast.error((e as { message?: string }).message ?? '生成失败'),
  });

  if (coursesQ.isLoading) {
    return (
      <div className="flex items-center justify-center py-24">
        <Spinner size={28} />
      </div>
    );
  }
  if (coursesQ.isError) {
    return (
      <div className="flex items-center justify-center py-24 text-sm text-muted">
        <AlertTriangle size={16} className="mr-2" />
        加载课程列表失败
      </div>
    );
  }

  const courses = coursesQ.data ?? [];

  return (
    <div>
      <PageHeader
        title="作业卷"
        description="按课程批量生成课后作业卷;支持屏上预览、显示参考答案、A4 打印。"
        breadcrumb={[{ label: '内容运营' }, { label: '作业卷' }]}
      />

      {/* 课程选择器 + 生成按钮(操作栏,no-print) */}
      <div className="no-print mb-4 flex flex-wrap items-end gap-3">
        <div className="min-w-[240px] flex-1">
          <label className="mb-1 block text-xs text-muted">选择课程</label>
          <select
            className="input"
            value={courseId ?? ''}
            onChange={(e) => {
              setCourseId(e.target.value ? Number(e.target.value) : null);
              setSelectedId(null);
            }}
          >
            <option value="">— 请选择 —</option>
            {courses.map((c) => (
              <option key={c.id} value={c.id}>
                {c.title}
                {c.subject ? `（${c.subject}）` : ''}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <button
            className="btn-primary"
            disabled={courseId == null || genMut.isPending}
            onClick={() => genMut.mutate()}
          >
            <Sparkles size={14} />
            {genMut.isPending ? '生成中…' : '为本课生成作业'}
          </button>
          <span className="text-[11px] text-muted">
            会为该课程下所有有素材的课时生成作业;已在途/已生成的课时会自动跳过。
          </span>
        </div>
      </div>

      {courseId == null ? (
        <EmptyState
          icon={<FileText size={28} />}
          title="选择一个课程开始"
          hint="选择课程后,可以批量生成作业卷并预览/打印。"
        />
      ) : homeworksQ.isLoading ? (
        <div className="flex items-center justify-center py-16">
          <Spinner size={24} />
        </div>
      ) : homeworksQ.isError ? (
        <div className="flex items-center justify-center py-16 text-sm text-bad">
          <AlertTriangle size={16} className="mr-2" />
          加载作业列表失败:{(homeworksQ.error as Error).message}
        </div>
      ) : homeworks.length === 0 ? (
        <EmptyState
          icon={<FileText size={28} />}
          title="该课程还没有作业"
          hint='点击上方"为本课生成作业"开始批量生成。'
        />
      ) : (
        // 注意:这个 grid 不能标 no-print —— 它内部含 .print-area(右侧预览的卷面),
        // 而 @media print 对 .no-print 用了 display:none,会把 print-area 一起藏掉。
        // 打印时其它元素靠 body * { visibility: hidden } 隐藏,侧栏也不例外,所以无需
        // 在这里标 no-print。
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[320px_1fr]">
          {/* 左侧:作业列表 */}
          <aside className="space-y-1">
            <div className="mb-2 text-xs font-medium text-muted">
              共 {homeworks.length} 份(点选预览)
            </div>
            {homeworks.map((h) => (
              <HomeworkListRow
                key={h.id}
                hw={h}
                active={h.id === effectiveSelectedId}
                episodeTitle={episodeTitle(h.episode_id)}
                onClick={() => setSelectedId(h.id)}
              />
            ))}
          </aside>

          {/* 右侧:预览 + 打印 */}
          <div>
            {effectiveSelectedId != null ? (
              <HomeworkPreview homeworkId={effectiveSelectedId} />
            ) : (
              <EmptyState title="选择左侧任一作业查看预览" />
            )}
          </div>
        </div>
      )}

      {/* 打印态:.print-area 只在 @media print 下可见。预览态里 HomeworkPreview
          已经渲染了同样的 HomeworkPrintView(套了 .no-print 的工具栏),这里再放一个
          干净的 print-area 副本给打印用,避免工具栏混进 PDF。
          —— 但 HomeworkPreview 内部已经把 HomeworkPrintView 标了 print-area,
          且工具栏标了 no-print,所以这里不需要第二个副本(否则会重复打印)。
          此处留空,实际打印走 HomeworkPreview 里的 .print-area。 */}
    </div>
  );
}

// ===========================================================================
// 列表行
// ===========================================================================
function HomeworkListRow({
  hw,
  active,
  episodeTitle,
  onClick,
}: {
  hw: Homework;
  active: boolean;
  episodeTitle: string;
  onClick: () => void;
}) {
  const isActive = hw.status === 'active';
  return (
    <button
      onClick={onClick}
      className={`flex w-full flex-col items-start gap-1 rounded-lg border px-3 py-2.5 text-left transition-colors ${
        active
          ? 'border-primary bg-card-2'
          : 'border-border/60 bg-card hover:bg-card-2/50'
      }`}
    >
      <div className="flex w-full items-center justify-between gap-2">
        <span className="truncate text-sm font-medium text-txt">{episodeTitle}</span>
        {isActive ? (
          <Tag color="#10b981">active</Tag>
        ) : (
          <Tag color="#94a3b8">archived</Tag>
        )}
      </div>
      <div className="flex w-full items-center justify-between text-xs text-muted">
        <span>v{hw.version}</span>
        <span>{formatDate(hw.created_at)}</span>
      </div>
    </button>
  );
}

// ===========================================================================
// 预览:屏上卷面 + 工具栏(显示答案 toggle / 打印按钮)
// ===========================================================================
function HomeworkPreview({ homeworkId }: { homeworkId: number }) {
  const [showAnswers, setShowAnswers] = useState(false);
  const viewQ = useQuery({
    queryKey: ['homework', homeworkId],
    queryFn: () => api.homeworkGet(homeworkId),
  });

  if (viewQ.isLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <Spinner size={24} />
      </div>
    );
  }
  if (viewQ.isError) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-bad">
        <AlertTriangle size={16} className="mr-2" />
        加载作业失败:{(viewQ.error as Error).message}
      </div>
    );
  }
  const view = viewQ.data;
  if (!view) return null;

  return (
    <div>
      {/* 工具栏(no-print):显示答案 toggle + 打印按钮 */}
      <div className="no-print mb-3 flex items-center justify-between gap-2">
        <div className="text-xs text-muted">
          版本 v{view.version} · 生成于 {formatDate(view.created_at)} · {view.sections.length} 大题
        </div>
        <div className="flex items-center gap-2">
          <button
            className="btn-ghost btn-sm inline-flex items-center gap-1.5"
            onClick={() => setShowAnswers((v) => !v)}
            title="切换参考答案显示"
          >
            {showAnswers ? <EyeOff size={14} /> : <Eye size={14} />}
            {showAnswers ? '隐藏答案' : '显示答案'}
          </button>
          <button
            className="btn-primary btn-sm inline-flex items-center gap-1.5"
            onClick={() => window.print()}
          >
            <Printer size={14} />
            打印
          </button>
        </div>
      </div>

      {/* 卷面。print-area 标记让 @media print 只显示这一块。屏上预览时它就是 .hw-paper。 */}
      <div className="print-area">
        <HomeworkPrintView view={view} showAnswers={showAnswers} withNameRow />
      </div>
    </div>
  );
}

// ===========================================================================
// 卷面渲染(屏上预览 + 打印共用)。props.withNameRow 控制是否渲染姓名栏
// (打印态要,纯屏上预览也可要——这里默认都给,屏上看到的就是打印效果)。
// ===========================================================================
export function HomeworkPrintView({
  view,
  showAnswers,
  withNameRow,
}: {
  view: HomeworkView;
  showAnswers: boolean;
  withNameRow?: boolean;
}) {
  return (
    <div className="hw-paper">
      <header className="hw-header">
        <h1>课后作业</h1>
        <div className="hw-meta-line">
          版本 v{view.version} · 生成时间 {formatDate(view.created_at)}
        </div>
        {withNameRow && (
          <div className="hw-name-row">
            <span>姓名:__________</span>
            <span>班级:__________</span>
            <span>学号:__________</span>
            <span>日期:__________</span>
            <span>得分:__________</span>
          </div>
        )}
      </header>

      {view.sections.map((section) => (
        <SectionView key={section.id} section={section} showAnswers={showAnswers} />
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// 大题:标题 + (可选)阅读材料 + 题目列表
// ---------------------------------------------------------------------------
function SectionView({
  section,
  showAnswers,
}: {
  section: HomeworkViewSection;
  showAnswers: boolean;
}) {
  return (
    <section className="hw-section">
      <h2 className="hw-section-title">
        {cnSeq(section.seq)}、{section.title}
      </h2>

      {section.passage_content && (
        <div className="hw-passage">
          {section.passage_title && <div className="hw-passage-title">{section.passage_title}</div>}
          <div>{section.passage_content}</div>
        </div>
      )}

      {section.questions.map((q) => (
        <QuestionView key={q.id} q={q} showAnswers={showAnswers} />
      ))}
    </section>
  );
}

// ---------------------------------------------------------------------------
// 题目:题干 + 作答区 + (可选)参考答案
// ---------------------------------------------------------------------------
function QuestionView({ q, showAnswers }: { q: HomeworkViewQuestion; showAnswers: boolean }) {
  return (
    <div className="hw-question">
      <div className="hw-question-stem">
        <span className="hw-seq">{q.seq}.</span>
        <span>{q.stem}</span>
        <span className="ml-2 text-[11px] text-slate-400">({TYPE_LABEL[q.type]})</span>
      </div>
      <AnswerArea q={q} />
      {showAnswers && <AnswerTag q={q} />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// AnswerArea:按 type 渲染作答区。集中处理 8 种题型。
// ---------------------------------------------------------------------------
function AnswerArea({ q }: { q: HomeworkViewQuestion }) {
  const scoring = parseScoring(q.scoring);
  const options = parseOptions(q.options);

  switch (q.type) {
    case 'choice':
    case 'multi_choice':
      return (
        <ol className={`hw-options ${q.type === 'multi_choice' ? 'multi' : ''}`}>
          {options.map((opt, i) => (
            <li key={i}>
              <span className="hw-option-mark" />
              <span className="hw-option-label">{String.fromCharCode(65 + i)}.</span>
              <span>{opt}</span>
            </li>
          ))}
        </ol>
      );

    case 'fill':
      // 题干通常自带 ____;这里额外补一条短横线兜底(若题干已有横线则视觉冗余但无害)。
      return (
        <div className="hw-answer-lines">
          <span className="hw-fill-blank" />
        </div>
      );

    case 'short_answer':
    case 'dictation':
    case 'translation':
      // 简答/默写/翻译:留 3 行横线。
      return (
        <div className="hw-answer-lines">
          <div className="hw-line" />
          <div className="hw-line" />
          <div className="hw-line" />
        </div>
      );

    case 'calculation':
      // 计算:大方框供列式。
      return <div className="hw-calc-box" />;

    case 'copy_word': {
      // 抄写:按 scoring.times 画格。默认 4 格。语文用田字格、英语用四线三格;
      // 这里无法从题型区分语种,统一用田字格(中文场景更常见);如果 scoring.content
      // 主要是拉丁字母,可以切四线三格——简化起见用田字格。
      const times = Math.max(1, scoring.times ?? 4);
      return (
        <div className="hw-tian-grid">
          {Array.from({ length: times }).map((_, i) => (
            <span key={i} className="hw-tian-cell" />
          ))}
        </div>
      );
    }

    default:
      return null;
  }
}

// ---------------------------------------------------------------------------
// AnswerTag:showAnswers=true 时在题旁浅色标注参考答案。
// ---------------------------------------------------------------------------
function AnswerTag({ q }: { q: HomeworkViewQuestion }) {
  const text = renderAnswerText(q.type, parseScoring(q.scoring));
  if (!text) return null;
  // choice/multi_choice 答案短,行内 tag;其余答案可能较长,用块状标注。
  if (q.type === 'choice' || q.type === 'multi_choice') {
    return <span className="hw-answer-tag">{text}</span>;
  }
  return <div className="hw-answer-block">{text}</div>;
}

// ---------------------------------------------------------------------------
// cnSeq:阿拉伯序号转中文一二三...(最多支持到 ~20,超出回退阿拉伯数字)。
// 用于大题标题"一、选择题"。简化实现,覆盖常见题量。
// ---------------------------------------------------------------------------
function cnSeq(n: number): string {
  const digits = ['零', '一', '二', '三', '四', '五', '六', '七', '八', '九', '十'];
  if (n <= 10) return digits[n];
  if (n < 20) return '十' + digits[n - 10];
  if (n === 20) return '二十';
  return String(n);
}
