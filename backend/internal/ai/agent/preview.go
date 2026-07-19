package agent

// preview.go 提供 admin "预览完整 prompt" 能力:把各 agent 的 system+user prompt
// 构造逻辑导出一份,供 handler 在不调 LLM 的前提下,把某课程+某 agent 最终会拼出的
// 完整 prompt 展示给 admin 看。admin 调优 hint 后立刻能看效果,不用等真生成。
//
// 为什么是 3 个独立导出函数,而不是一个 switch:
//   各 agent 的 Request 类型不同(SummarizerRequest/QuizzerRequest/AdviceRequest),
//   签名难统一成 `PreviewPrompt(agent, req interface{})`。3 个独立函数虽然有点重复,
//   但类型安全、调用方一目了然(handler 直接构造对应 Request 调对应函数,没有 reflect 之类
//   的运行时惊喜)。每个函数内调用对应的小写 build*UserPrompt,保证预览拼出的 user prompt
//   和真生成时一字不差——预览即真相。
//
// 函数都返回 (system, user) 两段:
//   - system 是该 agent 的 system prompt const(代码常量,这里冗余返回一份供 handler
//     一并展示);
//   - user 是 build*UserPrompt 拼装结果(含注入的 SummaryHint/QuizHint/AdviceHint/TermDict)。
//
// masterySeed/ExtraContext 等运行时字段在预览里留空:预览的目的是看 prompt 骨架 +
// hint/TermDict 解析效果,具体某学生的 mastery 不是预览重点。

// BuildSummaryPromptForPreview 拼出 summary agent 的完整 prompt(不调 LLM)。
// system = SummarizerSystemPrompt(代码常量);user = buildSummaryUserPrompt(req)。
// req 里只需填 Subject/SummaryHint/TermDict/Chunks 这些 prompt 字段;EpisodeID/CourseID
// 用于显示。预览时 Chunks 可以为空(prompt 会显示"字幕内容"段标题但无内容),handler
// 通常会塞一两条占位字幕或就让它空着。
func BuildSummaryPromptForPreview(req SummarizerRequest) (system, user string) {
	return SummarizerSystemPrompt, buildSummaryUserPrompt(req)
}

// BuildQuizPromptForPreview 拼出 quiz agent 的完整 prompt(不调 LLM)。
// system = QuizzerSystemPrompt;user = buildQuizUserPrompt(req, "") —— 预览不针对具体学生,
// masterySeed 留空(prompt 会显示"新学生,暂无答题记录",这正是预览场景的合理默认)。
func BuildQuizPromptForPreview(req QuizzerRequest) (system, user string) {
	return QuizzerSystemPrompt, buildQuizUserPrompt(req, "")
}

// BuildAdvicePromptForPreview 拼出 advice agent 的完整 prompt(不调 LLM)。
// system = AdviceSystemPrompt;user = buildAdviceUserPrompt(req, "") —— masterySeed 留空,
// 预览时 prompt 会显示"当前无答题记录"段,这正是预览场景的合理默认。
func BuildAdvicePromptForPreview(req AdviceRequest) (system, user string) {
	return AdviceSystemPrompt, buildAdviceUserPrompt(req, "")
}
