package jsonx

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestParseLLMJSON 表驱动测试 ParseLLMJSON 的整条兜底链:
// 正常 JSON / 围栏包裹 / 散文夹带 / 截断 / 裸引号(成对) / 裸引号(孤例) /
// 中文引号原样保留 / repair 后仍失败 / 空输入。
func TestParseLLMJSON(t *testing.T) {
	type holder struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}

	tests := []struct {
		name     string
		raw      string
		wantName string
		wantNote string
		wantRep  bool // 期望是否靠 repair 救回
		wantErr  bool
	}{
		{
			name:     "合法 JSON 直接解析",
			raw:      `{"name":"象棋","note":"楚河汉界"}`,
			wantName: "象棋",
			wantNote: "楚河汉界",
			wantRep:  false,
		},
		{
			name:     "围栏 ```json 包裹",
			raw:      "```json\n{\"name\":\"象棋\",\"note\":\"楚河汉界\"}\n```",
			wantName: "象棋",
			wantNote: "楚河汉界",
			wantRep:  false,
		},
		{
			name:     "散文夹带(前缀 prose + 后缀)",
			raw:      `好的,这是结果:{"name":"象棋","note":"楚河汉界"} 希望有帮助!`,
			wantName: "象棋",
			wantNote: "楚河汉界",
			wantRep:  false,
		},
		{
			name: "成对裸引号 → repair 救回",
			// string value 里两处裸 ASCII 双引号(引语),成对,repair 替换成「」后合法。
			raw:      `{"name":"象棋","note":"级别"毕业"了"}`,
			wantName: "象棋",
			wantNote: `级别「毕业」了`,
			wantRep:  true,
		},
		{
			name:     "成对裸引号后跟逗号(summary ep2 故障形态)",
			raw:      `{"name":"象棋","note":"象棋级别"毕业",之后"}` + "\n}",
			wantName: "象棋",
			wantNote: "", // pair-aware 修复后能 parse,但 note 内容被 repair 改写,这里只验 repaired+不报错
			wantRep:  true,
		},
		{
			name:    "孤例裸引号(单边)→ repair 改了但仍可能失败,不崩溃",
			raw:     `{"name":"象棋","note":"只开不闭"了}`,
			wantErr: true, // 单边引号是盲区,repair 后仍 parse 失败是合法结果
		},
		{
			name:     "中文引号「」原样保留(repair 不触发)",
			raw:      `{"name":"象棋","note":"级别「毕业」了"}`,
			wantName: "象棋",
			wantNote: "级别「毕业」了",
			wantRep:  false,
		},
		{
			name:    "空输入报错",
			raw:     ``,
			wantErr: true,
		},
		{
			name:    "纯散文无 JSON 报错",
			raw:     `今天天气不错`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h holder
			rep, err := ParseLLMJSON(tt.raw, &h)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望报错,实际成功 rep=%v name=%q note=%q", rep, h.Name, h.Note)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外失败: %v", err)
			}
			if rep != tt.wantRep {
				t.Errorf("repaired: 期望 %v, 实际 %v", tt.wantRep, rep)
			}
			if tt.wantName != "" && h.Name != tt.wantName {
				t.Errorf("name: 期望 %q, 实际 %q", tt.wantName, h.Name)
			}
			if tt.wantNote != "" && h.Note != tt.wantNote {
				t.Errorf("note: 期望 %q, 实际 %q", tt.wantNote, h.Note)
			}
		})
	}
}

// TestExtractJSONObject 单独钉住 extract 的围栏剥离 + 截断兜底行为。
func TestExtractJSONObject(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"无围栏", `{"a":1}`, `{"a":1}`},
		{"json 围栏", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"裸围栏", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"前缀散文", `结果:{"a":1} 完`, `{"a":1}`},
		{"嵌套对象取首个平衡", `{"a":{"b":2}}尾部`, `{"a":{"b":2}}`},
		{"截断:未闭合对象补 }", `{"a":1,"b":[1,2`, `{"a":1,"b":[1,2]}`},
		{"截断:未闭合字符串先补引号", `{"a":"abc`, `{"a":"abc"}`},
		{"无对象返回原样", `hello`, `hello`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractJSONObject(tt.raw)
			if got != tt.want {
				t.Errorf("\n got: %s\nwant: %s", got, tt.want)
			}
			if tt.want != "hello" {
				// 期望产物应是合法 JSON(extract 不保证语义正确,但结构合法)
				var v any
				if err := json.Unmarshal([]byte(got), &v); err != nil {
					t.Errorf("extract 产物不是合法 JSON: %v (got=%s)", err, got)
				}
			}
		})
	}
}

// TestRepairBareQuotesInJSON 钉住 repair 的成对替换 + 幂等性。
func TestRepairBareQuotesInJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"无裸引号-幂等", `{"a":"b"}`, `{"a":"b"}`},
		{"成对引语", `{"a":"级别"毕业"了"}`, `{"a":"级别「毕业」了"}`},
		{"已转义引号不动", `{"a":"他说\"你好\""}`, `{"a":"他说\"你好\""}`},
		{"多个独立 string 各自配对", `{"a":"x"y"","b":"p"q""}`, `{"a":"x「y」","b":"p「q」"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepairBareQuotesInJSON(tt.in)
			if got != tt.want {
				t.Errorf("\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

// TestRepairIdempotent 对无裸引号的合法 JSON,repair 输出与输入逐字节相同。
// ParseLLMJSON 据此判断"是否真的修过"(== 比较的契约)。
func TestRepairIdempotent(t *testing.T) {
	cases := []string{
		`{"a":"b"}`,
		`{"name":"象棋","note":"楚河汉界"}`,
		`{"list":["a","b"],"n":1}`,
	}
	for _, c := range cases {
		if got := RepairBareQuotesInJSON(c); got != c {
			t.Errorf("对合法 JSON 不应改动:\n in: %s\nout: %s", c, got)
		}
	}
}

// TestParseLLMJSON_RealWorldCase 复刻生产故障形态:summary 里 "象棋级别"毕业""。
// 这是本包存在的核心理由,单列一条防回归。
func TestParseLLMJSON_RealWorldCase(t *testing.T) {
	// 模拟 summary LLM 输出片段:keypoints 数组里一项含裸引号。
	raw := `{
  "headline": "象棋入门",
  "keypoints": [
    "象棋级别"毕业"的判断标准",
    "楚河汉界的由来"
  ]
}`
	type pt struct {
		Headline  string   `json:"headline"`
		KeyPoints []string `json:"keypoints"`
	}
	var got pt
	rep, err := ParseLLMJSON(raw, &got)
	if err != nil {
		t.Fatalf("应靠 repair 救回,却失败: %v", err)
	}
	if !rep {
		t.Error("期望 repaired=true")
	}
	if len(got.KeyPoints) != 2 {
		t.Fatalf("keypoints 数量: 期望 2, 实际 %d", len(got.KeyPoints))
	}
	if !strings.Contains(got.KeyPoints[0], "毕业") {
		t.Errorf("第一项内容异常: %q", got.KeyPoints[0])
	}
}

// TestExtractJSONObjectTruncatedUTF8MidChar 验证 MaxTokens 截断点落在中文 UTF-8
// 多字节字符中间(切了 1-2 字节)时的处理:s[start:] 带半个 UTF-8 序列会让后续
// json.Unmarshal 报 "invalid UTF-8"。ToValidUTF8 剔除残缺字节,保证喂给 Unmarshal
// 的是合法 UTF-8。
//
// 场景:LLM 正在写 {"a":"老师讲到中... 字符串值没闭合就被砍断,且砍断点恰好落在
// 「中」字(\xe4\xb8\xad)的第 2 字节后(留了 \xe4\xb8 两个残缺字节)。
// 期望:残缺字节剔除 + 字符串补闭合 + object 补闭合 → {"a":"老师讲到"}
func TestExtractJSONObjectTruncatedUTF8MidChar(t *testing.T) {
	input := "{\"a\":\"老师讲到" + string([]byte{0xe4, 0xb8})
	recovered := ExtractJSONObject(input)

	if !utf8.ValidString(recovered) {
		t.Errorf("recovered is not valid UTF-8: % x", []byte(recovered))
	}
	var v struct {
		A string `json:"a"`
	}
	if err := json.Unmarshal([]byte(recovered), &v); err != nil {
		t.Fatalf("recovered JSON unparseable: %v\nrecovered=%q", err, recovered)
	}
	if !strings.Contains(v.A, "老师讲到") {
		t.Errorf("expected 老师讲到 in salvaged value, got %q (recovered=%s)", v.A, recovered)
	}
}
