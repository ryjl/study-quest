package service

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
)

// ai_service_run_record.go 集中三个"纯文本输出型"AI 能力(advice / course_summary /
// user_report)写 ai_run 的共性。它们的 recorder 签名一致、填同样的 13 个字段、仅
// capability 串和 preview 截断(jsonKey + 字数上限)不同,所以抽一个 helper + 一个
// 通用截断函数,各 recorder 退化成薄 wrapper。
//
// 不在这个 helper 里的能力:
//   - quiz(homework 同 shape):persist 不是单行 upsert,且 draft 是结构化题目,preview
//     走 truncateForRun([]QuestionDraft),签名不同。
//   - polish / segment / summary:preview 是多字段 stats map,shape 完全不同。

// recordAgentRun 是 advice / course_summary / user_report 共享的 ai_run 写入。preview
// 由调用方用 truncateTextPreview 预先算好(各自的 jsonKey + 字数上限),capability 标明
// 这条 run 归属哪个能力。systemPrompt/userPrompt 是本次发给 LLM 的开场 prompt,写进
// ai_runs.system_prompt_text / user_prompt_text 供 admin「查看回放」。
//
// CreateRun 失败统一记日志(以前只有 recordAdviceRun 记,另两个吞掉——三处行为对齐,
// 避免静默丢失观测行,这是有意的统一)。
func (s *aiService) recordAgentRun(capability, preview string, jobID uint, modelName string, trace []agent.TraceStep, usage ai.Usage, turns int, elapsed time.Duration, result, note, systemPrompt, userPrompt string) {
	if err := s.contentRepo.CreateRun(&model.AIRun{
		JobID:            jobID,
		Capability:       capability,
		InputJSON:        fmt.Sprintf(`{"job_id":%d,"turns":%d,"steps":%d}`, jobID, turns, len(trace)),
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelUsed:        modelName,
		ResponseText:     preview,
		TraceJSON:        agent.TraceJSON(trace),
		SelfCheckResult:  result, // 这三种能力无 self-check,复用该列存 pass/fail
		SelfCheckNote:    note,
		DurationMs:       int(elapsed.Milliseconds()),
		SystemPromptText: systemPrompt,
		UserPromptText:   userPrompt,
	}); err != nil {
		log.Printf("AI: recordAgentRun(%s) failed for job %d: %v", capability, jobID, err)
	}
}

// truncateTextPreview 把文本截到 limit 字并包成 `{jsonKey: "..."}` 预览写入
// ai_run.response_text。limit 按各能力调(advice/user_report 400、course_summary 500);
// jsonKey 是 admin 前端按 capability 渲染预览时读的字段名。
func truncateTextPreview(text string, limit int, jsonKey string) string {
	if len([]rune(text)) > limit {
		text = string([]rune(text)[:limit]) + "…"
	}
	preview, _ := json.Marshal(map[string]any{
		jsonKey: text,
	})
	return string(preview)
}
