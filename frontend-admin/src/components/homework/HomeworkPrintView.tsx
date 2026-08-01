// HomeworkPrintView.tsx — 作业卷卷面渲染(屏上预览 + 打印共用)。
//
// 排版:中性现代风 · 小学高年级。共享组件,供 HomeworkPreviewModal(RegenTab 行内
// 「查看作业」按钮打开)复用。
//
// 排版设计要点(详见 homework.css 顶部注释):
//   - 卷头:课程名 + 课时标题 + 姓名/日期简洁信息栏(无考试元素)
//   - choice/multi_choice:2×2 网格
//   - fill:按题干 ____ 数量动态给横线
//   - calculation:130px 大方框
//   - copy_word:首行范字(楷体红色)+ 后续空格描红
//   - 答案版:choice 正确项高亮,其他题型蓝色斜体标作答区上方
//   - 页脚页码(打印态)
//
// 这是个纯展示组件,所有数据从 props 传(view + 可选的 course/episode 标题)。
import type {
  HomeworkQuestionType,
  HomeworkView,
  HomeworkViewQuestion,
  HomeworkViewSection,
} from '../../lib/types';
import './homework.css';

// ---------------------------------------------------------------------------
// 题型中文 label(用于题干小字提示 + 答案标注)
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
// countFillBlanks:数题干里 ____ (连续下划线,>=2 个)出现的次数,给等量横线。
// 题干可能写 "今天天气 ____，温度 ____" → 2 个填空 → 给 2 条横线。
// ---------------------------------------------------------------------------
function countFillBlanks(stem: string): number {
  const matches = stem.match(/_{2,}/g);
  return matches ? matches.length : 1; // 至少 1 条兜底
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
// HomeworkPrintView — 卷面主体。屏上预览 + 打印共用。
// ===========================================================================
export function HomeworkPrintView({
  view,
  courseTitle,
  subjectLabel,
  episodeTitle,
  showAnswers,
}: {
  view: HomeworkView;
  /** 课程标题(卷头小字)。可选,无则不显示该行。 */
  courseTitle?: string;
  /** 学科 label(如「数学」「语文」),拼到课程行。 */
  subjectLabel?: string;
  /** 课时标题(卷头主标题)。可选,无则用「课后作业」兜底。 */
  episodeTitle?: string;
  /** 是否显示参考答案(家长/老师批改视角)。 */
  showAnswers: boolean;
}) {
  // 卷头第一行:课程名 + 学科。
  const courseLine = [courseTitle, subjectLabel].filter(Boolean).join(' · ');

  return (
    <div className="hw-paper">
      <header className="hw-header">
        {courseLine && <div className="hw-course-line">{courseLine}</div>}
        <h1 className="hw-episode-title">{episodeTitle ?? '课后作业'}</h1>
        <div className="hw-info-row">
          <span>
            姓名<span className="hw-blank" />
          </span>
          <span>
            日期<span className="hw-blank" />
          </span>
        </div>
      </header>

      {view.sections.map((section) => (
        <SectionView key={section.id} section={section} showAnswers={showAnswers} />
      ))}

      {/* 页脚页码:打印态由 @media print 显示;屏上 display:none */}
      <div className="hw-page-footer" />
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
      <h2 className="hw-section-title">{sectionTitleWithSeq(section.seq, section.title)}</h2>

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

// sectionTitleWithSeq 智能拼大题标题:如果 LLM 已在 title 里写了序号前缀(如「一、选择题」
// 「二、填空题」「1、选择题」「1. 选择题」),就不重复加;否则补 cnSeq(seq)+「、」前缀。
// prompt 示例的 title 格式是「一、选择题」,LLM 照抄带序号,组件又加一遍会重复
// (「二、二、填空题」)。正则识别已有序号前缀(中文数字或阿拉伯数字,后跟「、」或「.」+ 可选空格)。
const sectionSeqPrefixRe = /^[\d一二三四五六七八九十]+[、.]\s*/;
function sectionTitleWithSeq(seq: number, title: string): string {
  const trimmed = title.trim();
  if (sectionSeqPrefixRe.test(trimmed)) {
    // 已含序号前缀,剥掉它再统一用 cnSeq 拼(规范化:LLM 写「1、」或「一、」都改成「一、」)。
    const withoutPrefix = trimmed.replace(sectionSeqPrefixRe, '');
    return `${cnSeq(seq)}、${withoutPrefix}`;
  }
  // 不含序号,补上(如 title 只是「填空题」→「二、填空题」)。
  return `${cnSeq(seq)}、${trimmed}`;
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
        <span className="hw-type-hint">({TYPE_LABEL[q.type]})</span>
      </div>
      <AnswerArea q={q} showAnswers={showAnswers} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// AnswerArea:按 type 渲染作答区。集中处理 8 种题型。
// v2 强化:choice 2×2 网格 + 正确项高亮、fill 动态横线、calculation 大方框、
//         copy_word 首行范字 + 描红、其他题型答案标作答区上方。
// ---------------------------------------------------------------------------
function AnswerArea({ q, showAnswers }: { q: HomeworkViewQuestion; showAnswers: boolean }) {
  const scoring = parseScoring(q.scoring);
  const options = parseOptions(q.options);

  switch (q.type) {
    case 'choice':
    case 'multi_choice': {
      const correctSet = new Set<number>();
      if (q.type === 'choice') {
        if (scoring.correct_index != null) correctSet.add(scoring.correct_index);
      } else {
        scoring.correct_indices?.forEach((i) => correctSet.add(i));
      }
      return (
        <ol className={`hw-options ${q.type === 'multi_choice' ? 'multi' : ''}`}>
          {options.map((opt, i) => (
            <li key={i} className={showAnswers && correctSet.has(i) ? 'is-correct' : ''}>
              <span className="hw-option-mark" />
              <span className="hw-option-label">{String.fromCharCode(65 + i)}.</span>
              <span>{opt}</span>
            </li>
          ))}
        </ol>
      );
    }

    case 'fill': {
      // v2:按题干 ____ 数量动态给横线。
      const blankCount = countFillBlanks(q.stem);
      return (
        <div className="hw-fill-area">
          {showAnswers && scoring.accept?.length ? (
            <div className="hw-answer-block">{renderAnswerText(q.type, scoring)}</div>
          ) : null}
          {Array.from({ length: blankCount }).map((_, i) => (
            <span key={i} className="hw-fill-blank" />
          ))}
        </div>
      );
    }

    case 'short_answer':
      return (
        <div className="hw-answer-lines">
          {showAnswers && scoring.reference ? (
            <div className="hw-answer-block">{renderAnswerText(q.type, scoring)}</div>
          ) : null}
          <span className="hw-prefix">答:</span>
          <div className="hw-line" />
          <div className="hw-line" />
          <div className="hw-line" />
        </div>
      );

    case 'dictation': {
      // 默写:按参考文本长度给 3-5 行。
      const refLen = scoring.reference?.length ?? 0;
      const lines = refLen > 100 ? 5 : refLen > 50 ? 4 : 3;
      return (
        <div className="hw-answer-lines">
          {showAnswers && scoring.reference ? (
            <div className="hw-answer-block">{renderAnswerText(q.type, scoring)}</div>
          ) : null}
          {Array.from({ length: lines }).map((_, i) => (
            <div key={i} className="hw-line" />
          ))}
        </div>
      );
    }

    case 'translation':
      return (
        <div className="hw-answer-lines">
          {showAnswers && scoring.reference ? (
            <div className="hw-answer-block">{renderAnswerText(q.type, scoring)}</div>
          ) : null}
          <span className="hw-prefix">译文:</span>
          <div className="hw-line" />
          <div className="hw-line" />
          <div className="hw-line" />
        </div>
      );

    case 'calculation':
      // calculation:130px 大方框(::before 显示「解:」)。答案标框上方。
      return (
        <>
          {showAnswers && scoring.reference ? (
            <div className="hw-answer-block">{renderAnswerText(q.type, scoring)}</div>
          ) : null}
          <div className="hw-calc-box" />
        </>
      );

    case 'copy_word': {
      // v2 强化:首行范字(楷体红色,从 scoring.content 取字)+ 后续空格描红。
      // 这是「像语文本」的关键。范字数 = content 的字符数(每字一格)。
      const content = scoring.content ?? '';
      const times = Math.max(1, scoring.times ?? 4);
      const modelChars = Array.from(content); // 按 Unicode code point 拆(中文安全)
      return (
        <div>
          {showAnswers && content ? (
            <div className="hw-answer-block">{renderAnswerText(q.type, scoring)}</div>
          ) : null}
          {modelChars.length > 0 && (
            <div className="hw-tian-grid">
              {modelChars.map((ch, i) => (
                <span key={`m${i}`} className="hw-tian-cell hw-tian-model">
                  {ch}
                </span>
              ))}
            </div>
          )}
          {Array.from({ length: times }).map((_, rowIdx) => (
            <div key={`r${rowIdx}`} className="hw-tian-grid">
              {modelChars.length > 0
                ? modelChars.map((_, i) => <span key={`r${rowIdx}c${i}`} className="hw-tian-cell" />)
                : Array.from({ length: 4 }).map((_, i) => (
                    <span key={`r${rowIdx}c${i}`} className="hw-tian-cell" />
                  ))}
            </div>
          ))}
        </div>
      );
    }

    default:
      return null;
  }
}

// ---------------------------------------------------------------------------
// cnSeq:阿拉伯序号转中文一二三...(最多支持到 ~20,超出回退阿拉伯数字)。
// 用于大题标题「一、选择题」。简化实现,覆盖常见题量。
// ---------------------------------------------------------------------------
function cnSeq(n: number): string {
  const digits = ['零', '一', '二', '三', '四', '五', '六', '七', '八', '九', '十'];
  if (n <= 10) return digits[n];
  if (n < 20) return '十' + digits[n - 10];
  if (n === 20) return '二十';
  return String(n);
}
