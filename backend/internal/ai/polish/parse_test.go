package polish

import (
	"strings"
	"testing"
)

// parse_test.go 测 parsePolishJSON 的容错。polish 之前是"双重裸奔"(手写
// first/last-brace 切片 + 无裸引号修复),接入 jsonx.ParseLLMJSON 后这里钉住
// 围栏剥离 / 裸引号救回 / 截断兜底三条防线,防回归。

// TestParsePolishJSON_HappyPath 合法 envelope + nil 切片初始化成空切片。
func TestParsePolishJSON_HappyPath(t *testing.T) {
	raw := `{"changes":[{"id":1,"edits":[{"find":"出军","replace":"出车"}]}]}`
	env, err := parsePolishJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Changes) != 1 || env.Changes[0].ID != 1 {
		t.Errorf("changes 解析异常: %+v", env.Changes)
	}
	// glossary 缺省应为空切片(非 nil),调用方 .append 安全。
	if env.Glossary == nil {
		t.Error("glossary 缺省应是空切片,不是 nil")
	}
	if len(env.Glossary) != 0 {
		t.Errorf("glossary 应为空,实际 %d 项", len(env.Glossary))
	}
}

// TestParsePolishJSON_StripsFences ```json 围栏剥离。
func TestParsePolishJSON_StripsFences(t *testing.T) {
	raw := "```json\n{\"changes\":[{\"id\":7,\"text\":\"修正后整句\"}]}\n```"
	env, err := parsePolishJSON(raw)
	if err != nil {
		t.Fatalf("围栏包裹应剥除: %v", err)
	}
	if len(env.Changes) != 1 || env.Changes[0].ID != 7 {
		t.Errorf("changes 异常: %+v", env.Changes)
	}
}

// TestParsePolishJSON_RecoversBareQuotes 防回归:LLM 在 text 字段(整句替换)里写
// 未转义裸 ASCII 双引号(引语),parsePolishJSON 走的 jsonx.ParseLLMJSON 应靠 repair
// 救回。polish 校对字幕时,模型在修正句子里引用术语会写裸引号(如"楚河汉界"),
// 之前双重裸奔会整批 parse 失败。这是接入统一 chokepoint 的核心保障。
func TestParsePolishJSON_RecoversBareQuotes(t *testing.T) {
	// text 字段含成对裸引号。第一次 unmarshal 失败,repair 替换成「」后救回。
	raw := `{"changes":[{"id":147,"text":"棋盘中央以"楚河汉界"分隔双方"}]}`
	env, err := parsePolishJSON(raw)
	if err != nil {
		t.Fatalf("应靠 repair 救回裸引号,却失败: %v", err)
	}
	if len(env.Changes) != 1 {
		t.Fatalf("期望 1 条 change 救回,实际 %d", len(env.Changes))
	}
	if env.Changes[0].ID != 147 {
		t.Errorf("id 异常: %d", env.Changes[0].ID)
	}
	if !strings.Contains(env.Changes[0].Text, "楚河汉界") {
		t.Errorf("text 内容异常(应含楚河汉界): %q", env.Changes[0].Text)
	}
}

// TestParsePolishJSON_BareQuoteInEdit edits.replace 里含裸引号也能救回。
func TestParsePolishJSON_BareQuoteInEdit(t *testing.T) {
	raw := `{"changes":[{"id":200,"edits":[{"find":"术语","replace":"楚河"汉界""}]}]}`
	env, err := parsePolishJSON(raw)
	if err != nil {
		t.Fatalf("edits 里裸引号应救回: %v", err)
	}
	if len(env.Changes) != 1 || len(env.Changes[0].Edits) != 1 {
		t.Fatalf("changes/edits 数量异常: %+v", env.Changes)
	}
}
