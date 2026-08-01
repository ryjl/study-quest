// Package ai 的 LLM 调用参数集中定义。
//
// 这是全项目 LLM 参数(max_tokens / temperature / max_steps / HTTP 超时)的**单一真相源**。
// 改之前散落在 6+ 个文件里的字面量,现在统一到这里命名,各生成点引用常量名而非魔法数字。
// 改值时只改这一个文件 + 重新部署。参数依据 + tuning 历史见 docs/modules/ai/llm-params.md。
//
// 设计决策(代码常量而非 DB 可配):这些参数调值不频繁(调对一次就稳定),DB 可配反而
// 引入 admin 误配风险(如 max_steps 调成 0 会让 agent 立刻失败)。需要运行时热调的
// 只有 polish_concurrency(并发数,受中转站限流影响,已在 settings 表单独可配)。
package ai

import "time"

// DefaultTemperature 是所有 LLM 调用的默认温度。
//
// 全仓库统一 0:这是教育产品,所有 LLM 任务都是 factual 抽取/出题/校对(总结要点、
// 出选择题、校对字幕错字),需要确定性而非创造性。temperature=0 让同输入尽量产生
// 同输出,便于复现问题和保证判分稳定性。生成类任务(如有创意写作)才需 >0。
const DefaultTemperature = 0.0

// ── MaxTokens(completion 上限,按 capability 区分)──────────────────────────
//
// 重要认知:MaxTokens 是**上限不是预留额度**——模型生成完即 stop(返回 finish_reason=
// "stop"),实际只消耗真实生成量的 token。设大**不会浪费 token**。设大 = 防截断安全垫;
// 设小 = 偶发长内容中途被砍断 → JSON 断裂 → 整次 parse 失败白烧几万 prompt token
// (代价远大于多生成几百 token)。故这些值**宁偏大**,只在有确凿实证超限时才提。
const (
	// MaxTokensSummary:课程总结(headline + sections + key_points + methods +
	// common_mistakes + takeaway + SVG,结构最重)。极端长课可能更高,16000 给足
	// 安全垫,绝不因截断失败。
	MaxTokensSummary = 16000

	// MaxTokensHomework:作业卷生成(多 section + 多题型 questions + scoring)。
	// 和 summary 同级——整卷输出结构重,保留同等安全垫。
	MaxTokensHomework = 16000

	// MaxTokensQuiz:quiz 出题(多道 choice/multi_choice/fill + explanation)。
	MaxTokensQuiz = 10000

	// MaxTokensQuizSelfCheck:quiz 自检的判决输出(pass/fail + 简短 note)。
	// 结构极简({"pass":bool,"note":"..."}),800 绰绰有余。
	MaxTokensQuizSelfCheck = 800

	// MaxTokensPolish:字幕校对(每 chunk 150 cues 的 changes/glossary diff)。
	// 12000 给足余量避免极端 chunk 被限。
	MaxTokensPolish = 12000

	// MaxTokensAdvice:学习建议(300-700 字,按 mastery 给方向性建议)。
	MaxTokensAdvice = 2500

	// MaxTokensUserReport:用户跨课程学习报告(串起多课程脉络,比 advice 略长)。
	MaxTokensUserReport = 3000

	// MaxTokensCourseSummary:课程级总结(串起整门课,300-700 字)。
	MaxTokensCourseSummary = 3000

	// MaxTokensProviderProbe:admin「实战测试」发的一个真实请求,验证 provider 配置可用。
	MaxTokensProviderProbe = 10000

	// MaxTokensPing:provider 连通性探测(只验证 round-trip,不要生成内容)。
	MaxTokensPing = 5
)

// ── MaxSteps(agent ReAct loop 步数上限,按 capability 区分)────────────────
//
// 每步是一次完整 LLM 调用(含全历史 messages,token 成本随步数累积)。这些值是
// "够用但不烧钱"的折中:太低 → 复杂任务没收集够信息就被强制结束(ErrMaxSteps);
// 太高 → 模型陷入循环时白烧 token。跑满上限会触发 forced final call(ToolChoice=none)
// 强制产出,仍空才 ErrMaxSteps(偶发,见 docs/pitfalls/backend.md「quiz max steps」)。
const (
	// MaxStepsQuiz:出题典型轨迹 get_episode_info → search_subtitles →
	// get_user_mastery → 输出 JSON,6 步够。偶发跑满(模型不收敛)重跑即可。
	MaxStepsQuiz = 6

	// MaxStepsAdvice:跨课程 mastery 数据量大,agent 可能多次调 get_*_mastery
	// 收集不同范围数据,留 10 步余量。
	MaxStepsAdvice = 10

	// MaxStepsUserReport:全课程遍历(比 advice 更广),同 10 步。
	MaxStepsUserReport = 10

	// MaxStepsCourseSummary:pre-seed 已喂所有 episode headline,agent 只需调
	// 1-3 次 get_episode_summary 深入关键 episode 即可写总结,8 步够。
	MaxStepsCourseSummary = 8

	// MaxStepsSelfCheck:单次判决(无工具,直接给 pass/fail),1 步。
	MaxStepsSelfCheck = 1
)

// ── HTTP 超时 / job deadline ──────────────────────────────────────────────
const (
	// HTTPClientTimeout 是 OpenAICompatProvider 的 http.Client 级超时:单次 LLM HTTP
	// 请求的硬墙。模型调用慢(长 prompt 生成长输出,10-60s 正常),120s 是"绝不误杀正常
	// 调用、又能兜底真正卡死的连接"的折中。agent 各步的 ctx deadline 叠加在这之上。
	HTTPClientTimeout = 120 * time.Second

	// SummaryJobTimeout 是 summary 单次尝试的 ctx deadline。长 prompt(整节课字幕 +
	// 多字段)正常生成 30-40s;原裸 context.Background() 唯一兜底就是上面的 120s,但
	// 正常长生成可能 >120s 被误杀。5min 给足正常空间,真正 hang 的请求由它兜底→重试。
	// (runSummaryJob 每次尝试用独立 ctx,失败重试 1 次,共 2 次尝试。)
	SummaryJobTimeout = 5 * time.Minute
)

// polish 的 deadline 自适应逻辑(max(chunk数×5min, 20min))+ maxRetries(2)+ 指数退避
// 因与断点续润/并发强耦合,保留在 internal/ai/polish/polish.go 内部定义,不搬到这里。
