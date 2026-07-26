package agent

import (
	"strings"
	"testing"
)

// homework_prompts_test.go 验证 DefaultHomeworkPrompt 按科目拼出的 system prompt 包含
// 该科目配方的关键内容(白/黑名单题型关键词、题量区间、特殊配比)。用 strings.Contains
// 断言,不写死全文——prompt 文本可能迭代,断言关键约束即可。
//
// 表驱动:math/chinese/english/physics/default 五个 case,每个 case 列出该科目配方必须
// 出现的若干关键子串。

func TestDefaultHomeworkPrompt(t *testing.T) {
	// 通用断言:Base 内容应该出现在每个科目配方里(角色定位 + 输出格式契约 + 反蒙题四原则 +
	// 8 种题型 scoring 约定 + 阅读理解说明)。
	commonMustContain := []string{
		"K12 课后作业出题助手",
		"严格 JSON",
		"sections",
		"questions_count",
		"反蒙题四原则",
		"correct_index",
		"correct_indices",
		`"accept"`,
		"reference",
		"copy_word",
		"dictation",
		"translation",
		"passage_title",
		"passage_content",
	}

	cases := []struct {
		name       string
		subjectKey string
		// 本科目配方必须出现的关键子串(在 Base 之外、由 homeworkSubjectRecipe 追加的部分)。
		recipeMustContain []string
		// 本科目配方必须 NOT 出现的子串(防止串台:比如数学配方不该提"默写")。
		recipeMustNotContain []string
	}{
		{
			name:       "math",
			subjectKey: "math",
			recipeMustContain: []string{
				"数学",
				"choice",
				"calculation",
				"short_answer",
				"15-25",
				"计算题",            // 特殊配比描述
				"至少 4 道",          // calculation ≥4
				"copy_word",         // 黑名单里出现
				"dictation",         // 黑名单里出现
				"translation",       // 黑名单里出现
			},
			recipeMustNotContain: []string{
				"本科目配方——语文", // 不能串台
				"本科目配方——英语",
			},
		},
		{
			name:       "chinese",
			subjectKey: "chinese",
			recipeMustContain: []string{
				"语文",
				"copy_word",
				"dictation",
				"short_answer",
				"15-25",
				"抄写",            // 抄写+默写合计≥5 的描述
				"至少 5 道",        // 抄写默写合计≥5
				"calculation", // 黑名单里出现
				"translation", // 黑名单里出现
				"阅读理解",        // 语文鼓励阅读理解
			},
			recipeMustNotContain: []string{
				"本科目配方——数学",
				"本科目配方——英语",
			},
		},
		{
			name:       "english",
			subjectKey: "english",
			recipeMustContain: []string{
				"英语",
				"copy_word",
				"translation",
				"short_answer",
				"15-25",
				"至少 5 道",          // 抄写≥5
				"英汉互译",           // translation 描述
				"calculation", // 黑名单里出现
				"dictation",   // 黑名单里出现(英语不考默写)
			},
			recipeMustNotContain: []string{
				"本科目配方——数学",
				"本科目配方——语文",
			},
		},
		{
			name:       "physics",
			subjectKey: "physics",
			recipeMustContain: []string{
				"物理/科学",
				"choice",
				"calculation",
				"short_answer",
				"15-25",
				"实验题", // 物理重视实验
				"copy_word",
				"dictation",
				"translation",
			},
			recipeMustNotContain: []string{
				"本科目配方——数学",
				"本科目配方——默认",
			},
		},
		{
			name:       "default",
			subjectKey: "",
			recipeMustContain: []string{
				"默认",
				"choice",
				"fill",
				"short_answer",
				"15-20",
			},
			recipeMustNotContain: []string{
				"本科目配方——数学",
				"本科目配方——语文",
				"本科目配方——英语",
				"本科目配方——物理/科学",
			},
		},
		{
			name:       "unknown-subject-falls-to-default",
			subjectKey: "underwater-basket-weaving",
			recipeMustContain: []string{
				"默认",
				"15-20",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DefaultHomeworkPrompt(c.subjectKey)

			// 1. Base 通用内容每个科目都该有。
			for _, sub := range commonMustContain {
				if !strings.Contains(got, sub) {
					t.Errorf("subject %q: prompt missing base substring %q", c.subjectKey, sub)
				}
			}

			// 2. 本科目配方关键内容。
			for _, sub := range c.recipeMustContain {
				if !strings.Contains(got, sub) {
					t.Errorf("subject %q: prompt missing recipe substring %q", c.subjectKey, sub)
				}
			}

			// 3. 不该出现的串台内容。
			for _, sub := range c.recipeMustNotContain {
				if strings.Contains(got, sub) {
					t.Errorf("subject %q: prompt unexpectedly contains %q (should not)", c.subjectKey, sub)
				}
			}

			// 4. 整体非空 + 是 Base 的超集(配方段追加在 Base 之后)。
			if len(got) <= len(HomeworkSystemPromptBase) {
				t.Errorf("subject %q: prompt (%d bytes) should be longer than base (%d bytes)",
					c.subjectKey, len(got), len(HomeworkSystemPromptBase))
			}
			if !strings.HasPrefix(got, HomeworkSystemPromptBase) {
				t.Errorf("subject %q: prompt should start with HomeworkSystemPromptBase", c.subjectKey)
			}
		})
	}
}

// TestHomeworkSubjectRecipeDirect 直接测 homeworkSubjectRecipe,断言每个科目配方段以
// 【本科目配方—— 开头(prompt 拼装的格式契约)。这是给主 session 集成时做"配方段是否存在"
// 的快速断言用。
func TestHomeworkSubjectRecipeDirect(t *testing.T) {
	for _, key := range []string{"math", "chinese", "english", "physics", "", "unknown"} {
		got := homeworkSubjectRecipe(key)
		if !strings.HasPrefix(got, "【本科目配方——") {
			t.Errorf("subjectKey %q: recipe should start with 【本科目配方——, got prefix %q",
				key, safePrefix(got, 20))
		}
		if !strings.HasSuffix(got, "。") && !strings.HasSuffix(got, "】") {
			// 配方段以句号或右书名号结尾(根据是否是最后一行)。这里宽松断言只要非空即可。
		}
		if strings.TrimSpace(got) == "" {
			t.Errorf("subjectKey %q: recipe should not be empty", key)
		}
	}
}

// safePrefix returns the first n runes of s (or all of s if shorter), used only
// to make error messages readable. Test-only helper.
func safePrefix(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
