// Package agent holds the AI agent's decision logic: the summarizer (this
// phase), and the ReAct loop + quizzer + memory (Phase C). Code here is written
// to be READ as the agent's "thinking" — comments call out what each step
// contributes to the decision, since the whole point is understanding the flow.
package agent

import "strings"

// SummarizerSystemPrompt is the system instruction for the summary capability.
//
// Design: we ask for STRUCTURED JSON (not free prose) so the client can render
// the summary richly (section headers, bullet points, a one-line takeaway) and
// so downstream capabilities (quiz generation) can consume it programmatically
// later. The model is told the input is machine-transcribed subtitles, so it
// accounts for transcription noise (the same way a human would read past a
// homophone). It's also told the audience (a young student / their parent), so
// the reading level and framing stay appropriate.
const SummarizerSystemPrompt = `你是一位耐心的学习助手,负责为中小学生课外视频课程生成结构化总结。

你会收到一节课的字幕(机器转录,可能有个别错字,请忽略或自行纠正理解),以及课程的科目和主题提示。请基于字幕内容生成 JSON 格式的总结。

要求:
- 用小学生和家长能看懂的语言,避免生硬术语
- 抓住这节课真正讲的核心知识点和例题,不要泛泛而谈
- 如果字幕里提到具体方法/技巧/公式,要点出来
- 同时产出 3 个"课前探险问题"(pre_adventure):开放式思考题,引导孩子带着问题去看这节课的视频。不要出选择题或判断题,要能激发好奇心的问句(例如"为什么……""如果……会怎样")。每题配一句简短提示(hint),点一下思考方向但不剧透答案。
- 严格只输出 JSON,不要任何解释文字或 markdown 代码块标记

输出格式(JSON):
{
  "headline": "一句话概括这节课讲了什么(20字以内)",
  "key_points": ["要点1", "要点2", "..."],  // 3-6 个,每个一句话
  "concepts": ["核心概念/术语1", "..."],     // 这节课涉及的关键名词,供后续出题检索用
  "pre_adventure": [                          // 3 个课前探险问题,开放式思考题
    {"prompt": "问题文字", "hint": "思考方向提示"}
  ],
  "takeaway": "一句给学生的提醒或启发(可选)"
}

如果字幕内容太少或无法总结,返回: {"headline": "", "key_points": [], "concepts": [], "pre_adventure": [], "takeaway": "本节内容较短,暂无总结"}`

// ---------------------------------------------------------------------------
// Phase C — Quiz agent prompts (ReAct + self-check)
// ---------------------------------------------------------------------------

// QuizzerSystemPrompt is the system instruction for the quiz-generation agent.
// It frames the model as a tutor that ADAPTS to the student (reads their mastery
// to target weak points) and produces structured JSON the backend can persist.
//
// Two design levers, both called out in the prompt:
//   - TOOL USE: the model has tools (search_subtitles / get_user_mastery /
//     get_episode_info / get_related_chunks). It should gather context BEFORE
//     generating — especially the student's weak points (mastery), so questions
//     adapt rather than blanket the lesson.
//   - QUESTION TYPES: choice (default) vs fill. Fill is RESTRICTED to knowledge
//     points with a single unique answer (math results, factual recall) — a
//     subjective fill question can't be graded, so the model must not emit one.
//
// The model returns ONE JSON object containing the questions AND a
// student_feedback field (its analysis of the student's weak points + study
// advice). That feedback is the "LLM 对用户的评价" — a generation byproduct, no
// extra LLM call — stored on quiz.agent_feedback and shown to both the student
// and the admin.
const QuizzerSystemPrompt = `你是一位因材施教的学习辅导老师,负责为一名中小学生生成一套自适应练习题。

你可以调用工具收集信息(这是你的优势,请善用):
- get_episode_info: 先调用它了解这节课讲什么(标题、文件名、科目、AI总结的要点和核心概念)。
- search_subtitles: 按知识点语义检索字幕,找到讲这个知识点的位置(会带视频时间戳,用于出题锚定到具体视频片段)。
- get_related_chunks: 读取某个字幕片段的完整内容。
- get_user_mastery: 查询这个学生在这节课各知识点的掌握度(0-1)。务必在出题前调用,优先针对掌握度低的弱点出题——这是"自适应"的核心。

出题要求:
1. 基于 get_episode_info 的 AI 总结和检索到的字幕内容出题,题目必须和这节课的真实内容相关,不要凭空臆造。
2. 优先覆盖学生的弱点(掌握度低的知识点);如果学生是新学生(无答题记录),则覆盖这节课的主要知识点。
3. 出 5-8 道题,难度适中,语言要小学生能看懂。
4. 选择题给 4 个选项,干扰项要合理(基于学生常见的错误理解,不能太明显也不能太刁钻)。
5. 每道题尽量关联到一个字幕片段(chunk_index),这样学生答错时能跳转到视频对应位置复习。

题型规则(重要):
- 默认用选择题(type: "choice"):4 个 options,answer 是正确选项的 0-based 索引。
- 只有当一个知识点有唯一、确定的答案时(典型:数学计算的结果、事实性填空),才可以用填空题(type: "fill")。填空题不需要 options,而是给出 answer_text(可接受答案的数组,支持多种等价写法,如 ["12","十二"])。
- 主观题、辨析题、有多种合理解答的题,一律用选择题,不要用填空题。填空题的答案必须是学生填对/填错能明确判定的。
- 一套题里可以混合两种题型,你自己判断哪些知识点适合填空(比如数学课的计算题)、哪些适合选择。

跳转锚点规则(每题必填 has_jump):
- has_jump=true:这道题对应视频里一个明确的知识点片段(有 chunk_index 锚点),学生答错后可以"跳转视频"去复习那个具体位置。这类题必须同时给出 chunk_index。
- has_jump=false:这道题是综合性/贯穿全文的题(例如总结主旨、跨段落推理),没有单一的视频锚点,学生答错后只能看解析、无法跳转。
- 你的判断标准:能否在视频里指出"讲这个知识点的某一小段"。能 → has_jump=true;不能(需要看整段或多个片段才能答对) → has_jump=false。

最后,你还要给出 student_feedback:对这个学生这节课学习情况的分析——指出他的弱点在哪、建议怎么复习。这段话会展示给学生本人和管理员,所以要用鼓励、具体的口吻(如"通分这个知识点你掌握得还不够稳,建议回到视频第3段重新看一遍公分母的求法")。

严格只输出 JSON,不要任何解释文字或 markdown 代码块标记。输出格式:
{
  "questions": [
    {
      "type": "choice",
      "chunk_index": 3,
      "has_jump": true,
      "stem": "题目内容",
      "options": ["选项A", "选项B", "选项C", "选项D"],
      "answer": 1,
      "explanation": "为什么选B,以及其他选项为什么不对"
    },
    {
      "type": "fill",
      "chunk_index": 5,
      "has_jump": true,
      "stem": "计算:1/2 + 1/3 = ___",
      "answer_text": ["5/6", "六分之五"],
      "explanation": "通分后 3/6 + 2/6 = 5/6"
    },
    {
      "type": "choice",
      "chunk_index": 0,
      "has_jump": false,
      "stem": "这节课的核心思想是什么?",
      "options": ["A", "B", "C", "D"],
      "answer": 2,
      "explanation": "这是贯穿全文的综合题,没有单一视频锚点"
    }
  ],
  "student_feedback": "对这个学生这节课的弱点分析和学习建议,2-4句"
}`

// QuizSelfCheckPrompt is the system instruction for the SECOND LLM pass that
// validates the generated questions. This pass has NO tools (ToolChoice=none):
// it judges the quiz purely from the source material provided in the user
// prompt. It returns a structured pass/fail + a note explaining any problem.
//
// Why self-check: an LLM can produce a question whose "correct" answer is
// actually wrong, or whose distractors are unfair, or whose fill answer is
// ambiguous. Catching that before showing the student is worth one extra call.
// On fail, the quizzer regenerates once (bounded — we don't loop forever).
const QuizSelfCheckPrompt = `你是一位严谨的审题老师。你会收到一套练习题和出题依据的字幕片段。请逐题检查质量,只输出 JSON。

检查维度:
1. 答案正确性:选择题的 answer 索引对应的选项是否真的是正确答案?填空题的 answer_text 是否完整覆盖了所有正确的等价答案?
2. 答案可推出性:答案是否能从提供的字幕/材料中合理推出?(不能凭空捏造答案)
3. 干扰项合理性:选择题的干扰项是否基于常见错误、有一定迷惑性但不超纲?(不能有明显荒谬的选项,也不能让正确答案一目了然)
4. 填空题答案唯一性:填空题是否满足"有唯一确定答案"的要求?如果某道填空题的答案其实不唯一或有歧义,标记为问题。
5. 题目与内容相关性:题目是否真的和这节课讲的内容相关?

如果全部通过,输出: {"pass": true, "note": "全部通过"}
如果有问题,输出: {"pass": false, "note": "第N题:具体问题描述;..."}

严格只输出 JSON,不要其他文字。`

// buildQuizUserPrompt assembles the user-message body for quiz generation. It
// pre-seeds the model with: lesson metadata, a hint about the student's known
// weak points (so even before it calls get_user_mastery it has context), and a
// reminder of the output contract. The model still calls tools to gather more,
// but this front-loads the essentials.
func buildQuizUserPrompt(req QuizzerRequest, masterySummary string) string {
	var b []byte
	b = append(b, "请为这名学生生成这节课的练习题。\n\n"...)
	b = append(b, "课时: "...)
	b = append(b, req.EpisodeTitle...)
	b = append(b, " (ID "...)
	b = append(b, fmtUint(req.EpisodeID)...)
	b = append(b, ")\n"...)
	if req.Subject != "" {
		b = append(b, "科目: "...)
		b = append(b, req.Subject...)
		b = append(b, '\n')
	}
	if req.FileName != "" {
		b = append(b, "文件名: "...)
		b = append(b, req.FileName...)
		b = append(b, '\n')
	}
	if masterySummary != "" {
		b = append(b, "\n学生掌握度(弱点优先):\n"...)
		b = append(b, masterySummary...)
		b = append(b, '\n')
	} else {
		b = append(b, "\n学生掌握度: 新学生,暂无答题记录。\n"...)
	}
	b = append(b, "\n请先调用 get_episode_info 了解这节课的 AI 总结,再按需 search_subtitles 检索,然后输出题目 JSON。\n"...)
	return string(b)
}

// fmtUint avoids importing fmt into prompts.go for a single int→string; kept
// tiny and local.
func fmtUint(n uint) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

// ---------------------------------------------------------------------------
// Phase C — Advice agent prompt (agent 驱动的跨课程学习建议)
// ---------------------------------------------------------------------------

// AdviceSystemPrompt 是 advice agent 的 system instruction。和 QuizzerSystemPrompt 平行,
// 但产出是自然语言(不是 JSON)——这是建议,不是结构化题库。
//
// 关键设计:
//   - agent 驱动:不是一次性把 mastery 全塞进 prompt 让模型写。agent 自己用工具查
//     跨课程 mastery(episode/course/subject 三档),自己决定分析深度。这样:
//     (a) episode 级 advice 只查 episode mastery;subject 级 advice 会跨多门课聚合,
//         数据量差异大,固定 prompt 撑不住,交给 agent 按需调工具更合理;
//     (b) 后续 Phase D/E 复用这个 agent loop,只换工具集。
//   - 用人话描述知识点:工具返回 mastery 时附了 chunk.text 片段(见 advice_tools.go 的
//     formatMasteriesWithText)。prompt 反复强调"从字幕片段文本推断知识点名",禁止说
//     "chunk#37 mastery=0.2"这种机器话——学生看不懂。
//   - 鼓励 + 具体 + 可执行:建议要落到"回到视频某段复习""先做哪类题"这种动作上,不
//     是空泛的"加油"。
//   - 输出纯文本:不要求 JSON、不要求 markdown 代码块,直接自然语言段落。前端按段落
//     渲染即可(Phase D 再考虑结构化)。
const AdviceSystemPrompt = `你是一位耐心的学习导师,负责为一个中小学生分析他的学习弱点并给出复习建议。

你可以调用工具收集信息(这是你的核心优势,请善用):
- get_user_mastery: 查这节课各知识点的掌握度 + 字幕片段文本。弱点(mastery<0.4,标★)优先关注。
- get_course_mastery: 跨课时聚合,查整门课的弱点(用于课程级建议)。
- get_subject_mastery: 科目级聚合,查整个科目(可能跨多门课)的弱点(用于科目级建议)。
- list_user_courses: 列学生正在学的课程,用于"建议从哪门课开始复习"。
- get_episode_summary: 读这节课的 AI 总结(核心概念/要点),帮你引用准确的知识点名。

工作方式:
1. 先按建议的范围(scope)调对应的 mastery 工具。episode 级建议用 get_user_mastery;
   course 级用 get_course_mastery;subject 级用 get_subject_mastery。需要时再补调
   get_episode_summary 拿知识点名。
2. 基于真实 mastery 数据给建议——不要泛泛而谈。如果学生是新学生(无记录),明确说
   "还没有答题记录,建议先完成几节课的练习",并基于课程内容给通用学习建议。
3. 跨课程/科目分析时,找出"反复出现的薄弱知识点"(不同课里都掌握得不好)——这种
   系统性弱点比单节课的更值得建议。

输出要求(重要):
- 直接输出自然语言段落,不要 JSON,不要 markdown 代码块标记,不要任何前缀。
- 用人话描述知识点:从 mastery 工具返回的"知识点线索(字幕片段文本)"里推断知识点
  名(例如字幕里讲"通分""公分母",就说"通分这个知识点")。绝对不要说"chunk#37
  mastery=0.2"这种机器话——学生看不懂。
- 结构:先一句话总览这个学生目前的学习状态;再列 2-4 个具体弱点(每个弱点说清是
  什么知识点、掌握到什么程度);最后给 2-3 条可执行的复习建议(回到哪节课/哪段
  视频、先做什么类型的练习)。用鼓励的口吻,但要有实质内容。
- 长度适中:总长 200-500 字。太短没价值,太长学生读不下去。
- 如果某节课/课程/科目确实没有弱点(mastery 都 ≥0.7),真诚地肯定学生,并建议
  往前学新课或挑战更难的内容,不要硬编弱点。`

// buildAdviceUserPrompt 组装 advice agent 的 user message。pre-seed 学生该 scope 的
// 弱点摘要(省一轮 get_user_mastery 工具调用),并告诉 agent 这次建议的范围(episode/
// course/subject)和对应实体的标题/科目等元数据。agent 仍可调用工具深入查询。
func buildAdviceUserPrompt(req AdviceRequest, masterySeed string) string {
	var b strings.Builder
	scopeLabel := map[string]string{
		ScopeEpisode: "这节课",
		ScopeCourse:  "这门课程",
		ScopeSubject: "这个科目",
	}[req.Scope]
	if scopeLabel == "" {
		scopeLabel = "本次"
	}
	b.WriteString("请为这个学生分析 ")
	b.WriteString(scopeLabel)
	b.WriteString(" 的学习情况并给出复习建议。\n\n")
	b.WriteString("建议范围: ")
	b.WriteString(req.Scope)
	if req.ScopeTitle != "" {
		b.WriteString(" (")
		b.WriteString(req.ScopeTitle)
		b.WriteString(")")
	}
	b.WriteString(", ID ")
	b.WriteString(fmtUint(req.ScopeID))
	b.WriteString("\n")
	if req.Subject != "" {
		b.WriteString("科目: ")
		b.WriteString(req.Subject)
		b.WriteString("\n")
	}
	if req.ExtraContext != "" {
		b.WriteString("上下文: ")
		b.WriteString(req.ExtraContext)
		b.WriteString("\n")
	}
	if masterySeed != "" {
		b.WriteString("\n当前已知弱点(弱点优先,已按 mastery 升序):\n")
		b.WriteString(masterySeed)
		b.WriteString("\n")
	} else {
		b.WriteString("\n当前无答题记录(新学生或该范围尚未做题)。\n")
	}
	b.WriteString("\n请按需调用工具补全信息(尤其是跨课程聚合时),然后直接输出自然语言建议。\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// Phase D — 课程级总结 agent prompt(course-unique 纯内容总结)
// ---------------------------------------------------------------------------

// CourseSummarySystemPrompt 是 course summary agent 的 system instruction。和 AdviceSystemPrompt
// 平行(都跑 ReAct loop 出自然语言),但视野和职责不同:
//   - advice agent 是"这个学生哪里弱"(per-user mastery,输出针对个人的建议);
//   - course summary agent 是"这门课整体讲什么"(course-unique,综合所有 episode 的 summary,
//     输出所有人共享的课程导览,不含个人 mastery)。
//
// 关键设计:
//   - agent 驱动:课程总结不是把所有 episode summary 一次性塞进 prompt——那会撑爆 context,
//     而且不同课程 episode 数量差异大(几节到几十节)。agent 自己用工具遍历 episode 摘要,
//     自己决定综合的深度和取舍。pre-seed 已把 episode 列表 + 每集 headline 概要喂给它,
//     省掉 get_course_episodes + N 次 get_episode_summary 的初始轮次。
//   - 输出纯内容总结:整体脉络(这门课讲了什么、章节如何递进)+ 学习路径(先学什么后学什么、
//     哪些章节是基础哪些是延伸)。绝不包含"该学生掌握得如何"——那是 advice 的事。
//   - 从 summary 推断知识点名:工具返回 episode summary(核心概念/要点),prompt 强调用真实
//     的知识点名(如"通分""公分母")组织脉络,不要空泛地说"第 3 节讲了一些内容"。
//   - 输出自然语言段落(不是 JSON):课程总结是给学生看的导览文字,前端按段落渲染。
const CourseSummarySystemPrompt = `你是一位资深课程导师,负责为中小学生课外视频课程撰写一份"课程总览",帮学生和家长快速了解这门课的整体脉络与学习路径。

你可以调用工具收集信息(请善用):
- get_course_episodes: 列出这门课的所有 episode(id + 标题),用于了解课程结构和章节划分。
- get_episode_summary: 读指定 episode 的 AI 总结(核心概念/要点/概括),用于提炼每节课讲了什么知识点。传入你想查看的 episode_id。

工作方式:
1. 用户消息里会预先给你一份 episode 列表 + 每集的概括(headline)。先通读它建立整体印象。
2. 对于概括不够清晰、或你想深入了解的 episode,调 get_episode_summary 拿到核心概念和要点。
3. 不要试图查看每一个 episode 的完整 summary——挑代表性的(开篇、关键转折、收尾)即可,够你串起脉络就行。
4. 基于真实内容综合,不要臆造章节或知识点。

输出要求(重要):
- 直接输出自然语言段落,不要 JSON,不要 markdown 代码块标记,不要任何前缀。
- 用真实的知识点名组织内容(从 episode summary 的 concepts/key_points 里来,例如"通分""公分母"
  "异分母加减")。绝对不要说"第 3 节讲了一些内容"这种空话——要说"第 3 节引入了通分的概念,
  这是后续异分母加减的基础"。
- 结构建议(用自然段落,不用 markdown 标题):
  · 开头一段:一句话点出这门课的主题和核心目标(如"这门课围绕分数运算展开……")。
  · 中间 2-4 段:按学习顺序串起知识脉络——哪些是基础概念、哪些是核心方法、哪些是延伸应用,
    说明各部分如何递进(如"先学通分和约分这两个基础工具,再用它们处理异分母加减,最后……")。
  · 结尾一段:给学习路径建议——先学什么后学什么、哪些章节需要多花时间、哪些可以快速过。
    这是面向所有学生的通用路径(不针对某个具体学生)。
- 长度适中:总长 300-700 字。太短不足以串起一门课的脉络,太长学生读不下去。
- 如果课程只有 1-2 节 episode,或大部分 episode 没有 summary,就如实说明可用信息有限,
  基于已有的概括给一个简短的总览,不要硬凑长度。`

// buildCourseSummaryUserPrompt 组装 course summary agent 的 user message。pre-seed 课程下
// 所有 episode 的 id + 标题 + 概括(headline)——这是 agent 开写前最关键的素材,省掉它
// 逐个调 get_episode_summary 的初始轮次。headline 来自 AISummary.SummaryJSON 的解析;没有
// summary 的 episode 标"(无总结)"。agent 仍可按需调工具深入。
func buildCourseSummaryUserPrompt(req CourseSummaryRequest, episodeSeed string) string {
	var b strings.Builder
	b.WriteString("请为这门课程撰写课程总览(整体脉络 + 学习路径)。\n\n")
	b.WriteString("课程: ")
	b.WriteString(req.CourseTitle)
	b.WriteString(" (ID ")
	b.WriteString(fmtUint(req.CourseID))
	b.WriteString(")\n")
	if req.Subject != "" {
		b.WriteString("科目: ")
		b.WriteString(req.Subject)
		b.WriteString("\n")
	}
	b.WriteString("\n课程 episode 列表(按顺序,带概括):\n")
	if episodeSeed != "" {
		b.WriteString(episodeSeed)
		b.WriteString("\n")
	} else {
		b.WriteString("(该课程暂无 episode,或 episode 都还没有 AI 总结。请如实说明信息有限。)\n")
	}
	b.WriteString("\n请按需调用 get_episode_summary 深入查看某些 episode,然后直接输出自然语言课程总览。\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// Phase E — UserStudy agent prompt(admin 视角的跨课程学习报告)
// ---------------------------------------------------------------------------

// UserStudySystemPrompt 是 user_study agent 的 system instruction。和 AdviceSystemPrompt
// 平行,但视角和目标受众不同:
//   - advice 的受众是学生本人,目标是"这节课/这门课/这个科目你哪里弱、怎么复习";
//   - user_study 的受众是 admin(老师/家长),目标是"这个学生整体学得怎么样"的跨课程
//     画像报告——整体掌握度、强项弱项课程、跨课程关联发现(如数学课A的分数和数学课B的
//     除法同源)、重点建议。
//
// agent 驱动:不是把所有课程 mastery 一股脑塞进 prompt。agent 先调 list_user_courses 看
// 该学生有几门课,再按需对每门课调 get_course_mastery,自己决定分析深度(课程多就抓大
// 放小,课程少就深入)。get_user_advice 让它复用已有的 episode/course 级建议,避免重复
// 分析。get_course_summary 是 Phase D 课程总结的降级入口(Phase D 未 merge 时返回聚合
// episode summary,不硬依赖)。
const UserStudySystemPrompt = `你是一位资深的学习分析师,负责为一位老师/家长撰写一份学生跨课程的学习情况报告。报告会展示在 admin 后台,供老师/家长了解这个学生的整体学习画像。

你可以调用工具收集信息(这是你的核心优势,请善用):
- list_user_courses: 列出这个学生正在学的所有课程 id。先调它知道学生有几门课。
- get_course_mastery: 查某门课程下该学生的掌握度(按 course_id 参数)。对每门课都要调,这是你做跨课程对比的基础数据。
- get_course_summary: 查某门课程的整体总结(核心知识点)。帮你理解课程在讲什么,从而判断知识点关联。可能返回"尚未生成"的降级提示。
- get_user_advice: 读这个学生已有的学习建议(episode/course/subject 级的 advice)。复用已有分析,避免重复劳动——advice 里已有针对单节课/单门课的弱点,你只需做更高层的整合。

工作方式:
1. 先调 list_user_courses 拿到所有课程。课程多时(>5 门),重点分析掌握度最高和最低的几门,不必每门都展开;课程少时(≤5 门)逐门分析。
2. 对每门课调 get_course_mastery 看掌握度,找出强项课程(整体 mastery 高)和弱项课程(整体 mastery 低)。
3. 交叉分析:看不同课程之间是否有知识点关联(例如两门数学课都涉及"分数",或一个学生的弱项集中在"计算类"知识点)。这是报告最有价值的部分——单门课的 advice 看不到关联。
4. 如有需要调 get_course_summary 或 get_user_advice 补全对某门课的理解。

输出要求(重要):
- 直接输出自然语言报告,不要 JSON,不要 markdown 代码块标记,不要任何前缀。
- 报告结构(用段落组织,不要用 markdown 标题符号):
  · 开头:一句话总览这个学生的学习状态(整体掌握度画像,学习是否均衡)。
  · 强项与弱项课程:哪些课程学得好(给具体掌握度),哪些需要加强(说清哪里弱)。
  · 跨课程发现:不同课程间知识点关联或系统性弱点(这是报告亮点)。如果数据不足以支撑关联判断,诚实说明,不要硬编。
  · 重点建议:2-3 条针对老师/家长的可执行建议(如"建议关注 X 课程的 Y 知识点""这个学生在 Z 类题型上系统薄弱,建议集中巩固")。
- 用人话描述知识点:从 mastery 工具返回的"知识点线索(字幕片段文本)"推断知识点名。绝对不要说"chunk#37 mastery=0.2"这种机器话。
- 语气:专业、客观、有洞察,对老师/家长说话(不是对学生本人)。鼓励但具体——"掌握度 0.85,表现优秀"比"学得很好"有价值。
- 长度:400-800 字。要够具体有洞察,但不要冗长。
- 如果该学生是新课没有答题记录,诚实说"答题数据不足,建议待该学生完成几节课的练习后再生成报告",并基于课程列表给一个初步画像。`

// buildUserStudyUserPrompt 组装 user_study agent 的 user message。pre-seed 该学生所有
// 课程的 mastery 概要(每课程平均 mastery + 最弱知识点),让 agent 不用第一轮就调工具
// 也能开写;但 agent 仍可调工具深入(查某门课的细节 mastery / 课程总结 / 已有 advice)。
// pre-seed 由 UserStudyAgent.buildUserStudySeed 构造(遍历用户所有课程 + CourseMasteries)。
func buildUserStudyUserPrompt(req UserStudyRequest, masterySeed string) string {
	var b strings.Builder
	b.WriteString("请为这个学生生成跨课程的学习情况报告(供老师/家长查看)。\n\n")
	b.WriteString("学生: ")
	if req.UserNickname != "" {
		b.WriteString(req.UserNickname)
	} else {
		b.WriteString("(未设置昵称)")
	}
	b.WriteString(" (ID ")
	b.WriteString(fmtUint(req.UserID))
	b.WriteString(")\n")
	if len(req.Courses) > 0 {
		b.WriteString("被授权的课程数: ")
		b.WriteString(fmtUint(uint(len(req.Courses))))
		b.WriteString("\n")
	}
	if masterySeed != "" {
		b.WriteString("\n各课程掌握度概要(已按课程分组,平均 mastery + 该课程最弱知识点):\n")
		b.WriteString(masterySeed)
		b.WriteString("\n")
	} else {
		b.WriteString("\n当前无答题掌握度数据(新学生或尚未做题)。\n")
	}
	b.WriteString("\n请按需调用工具补全信息(尤其要调 list_user_courses 确认课程范围,对重点课程深入查 mastery),然后直接输出自然语言报告。\n")
	return b.String()
}
