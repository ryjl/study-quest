// Package jsonx 解析 LLM 自由文本里产出的 JSON,是全项目处理 LLM JSON 的统一
// chokepoint(唯一入口)。所有解析 LLM 返回 JSON 的点(quiz/homework/summary/polish
// 及工具参数)都应走 ParseLLMJSON,不要各自手写 json.Unmarshal。
//
// 为什么单独建一个公共包(而非塞进 agent 业务包):
//   - agent 和 polish 是两个平行业务包,把纯解析工具塞进任一个都会制造跨业务包依赖;
//     jsonx 是无业务依赖的纯函数包,agent/polish/service 都能 import,不会循环。
//   - 解析逻辑(围栏剥离/截断兜底/裸引号修复)是 LLM 输出的通用容错,与业务无关,
//     单独成包便于测试和以后扩展(如接入 jsonrepair 库或 response_format 检测)。
//
// 三层防御(本包负责后两层;第一层 response_format 由调用方在 provider 层启用):
//  1. 【源头/根治-当前未启用】response_format:{type:"json_schema",strict:true} ——
//     约束解码时模型物理上发不出裸引号(grammar 在 string 内禁止裸 ")。需后端支持
//     xgrammar/guidance/outlines 或原生 OpenAI gpt-4o+。探测(2026-07-29)确认当前
//     中转站 api.ja.870314.xyz 及后端模型 llmy 均 400 拒绝该参数(unsupported_parameter),
//     故此层暂不可用;换后端后从 provider 层统一启用,本包的 repair 兜底继续保留。
//  2. 【兜底/主防线-当前生效】ParseLLMJSON:extract(围栏/截断)→ unmarshal →
//     失败则 RepairBareQuotes(裸引号)→ 再 unmarshal。覆盖绝大多数裸引号故障。
//  3. 【辅助】prompt 强化:要求 LLM 输出 JSON 时用中文引号「」代替 ASCII 引号。
//     软约束,降低裸引号频率但不消除,作为第 2 层的减负。
//
// 局限:repair 是启发式(猜裸 " 是真结束还是引语),有成对引语盲区外的歧义场景
// (单边引号、复杂嵌套)救不回,最终仍可能 parse 失败,靠调用方重试兜底。这是
// 在后端不支持 response_format 时能做的极限;根治需换后端模型启用第 1 层。
package jsonx

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseLLMJSON 解析 LLM 自由文本里产出的 JSON,带围栏剥离 + 截断兜底 + 裸引号修复。
//
// 兜底链:
//  1. ExtractJSONObject —— 剥 ```json 围栏、挖首个平衡 {…}、截断兜底(闭合被
//     MaxTokens 砍断的未完结结构,救回前面 N-1 个完整项)。
//  2. json.Unmarshal —— 正常路径,绝大多数命中。
//  3. 失败则 RepairBareQuotesInJSON —— 把 string value 里的裸 ASCII 双引号
//     (LLM 高频故障:象棋级别"毕业")启发式替换成中文「」(成对交替),再 unmarshal。
//
// 入参 v 传指针(如 &result),与 json.Unmarshal 一致。
// 返回 (repaired, err):repaired=true 表示靠引号修复救回(第一次失败 + repair 后成功),
// 供调用方在 ai_runs 留痕观测 LLM 多频繁写裸引号。repair 未触发(正常成功)或 repair
// 后仍失败,都返回 repaired=false。
func ParseLLMJSON(raw string, v any) (repaired bool, err error) {
	s := ExtractJSONObject(raw)
	if uerr := json.Unmarshal([]byte(s), v); uerr == nil {
		return false, nil
	} else {
		// 第一次 parse 失败,常见根因是 string value 里的裸 ASCII 双引号。尝试修复。
		fixed := RepairBareQuotesInJSON(s)
		if fixed == s {
			// repair 没改动任何字符(无裸引号可修)→ 失败是其它原因(真截断/语法错),
			// 返回原始 unmarshal 错误,不误导调用方以为修过。
			return false, fmt.Errorf("invalid LLM JSON: %w", uerr)
		}
		if err2 := json.Unmarshal([]byte(fixed), v); err2 != nil {
			// repair 改了但仍 parse 失败(裸引号盲区:单边引号/复杂嵌套)。错误信息
			// 用原始 err(uerr),不含修复版片段(避免泄露 LLM 原文,诊断走 ai_runs)。
			return false, fmt.Errorf("invalid LLM JSON (repair attempted but still failed): %w", uerr)
		}
		return true, nil
	}
}

// ExtractJSONObject 从模型自由文本响应里挖出首个平衡的 JSON 对象。
//
// 中转站/模型经常把结构化输出裹在 ```json 围栏里,或夹带散文/尾部内容
// ("Here is the quiz: {...}. Good luck!")。朴素的"首个 { 到最后 }"切片在模型
// 追加了第二个对象或尾部括号时会出错——所以这里走一遍字符串、追踪开括号栈,
// 在首个平衡的闭合处停止。JSON 内部的字符串字面量(可能含括号)通过追踪转义状态
// 被正确尊重,不会污染栈。
//
// 截断兜底(truncation recovery):若走到字符串末尾仍未平衡(输出被中途砍断——
// 典型是 max_tokens 上限落在多字节 UTF-8 字符中间,表现为
// "invalid character 'é' after object key:value pair"),不直接返回残缺 JSON 让整次
// 解析失败,而是尽力补全:先闭合未完结的字符串字面量,再按未闭合的开符号栈逆序补对应
// 的闭符号(} 配 {,] 配 [)。补全后的 JSON 能被 Unmarshal 解析,逐题校验的调用方会
// 丢弃最后一道写了一半的残题,从而救回前面 N-1 道完整题——比整次 run 失败白烧几万
// token 强得多。这是最后一道保险:首选仍是靠足够的 MaxTokens 让输出不被砍断。
func ExtractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	// Strip ```json ... ``` fences if present (common with some models).
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	if strings.HasSuffix(s, "```") {
		s = s[:len(s)-len("```")]
	}
	s = strings.TrimSpace(s)

	start := strings.Index(s, "{")
	if start < 0 {
		return s // no object at all — let the caller's Unmarshal report it
	}
	// Walk from start tracking an open-bracket STACK of { and [ (tracking both,
	// not just a { depth counter, so a truncated array like ["A","B can be
	// closed correctly in the fallback below). String literals + escapes are
	// honored so a brace inside a string value (e.g. an explanation containing
	// "{}") doesn't corrupt the stack.
	var openStack []byte
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '{', '[':
			openStack = append(openStack, c)
		case '}', ']':
			if len(openStack) > 0 {
				want := byte('{')
				if c == ']' {
					want = '['
				}
				if openStack[len(openStack)-1] == want {
					openStack = openStack[:len(openStack)-1]
					if len(openStack) == 0 {
						return s[start : i+1] // first balanced object
					}
				}
			}
		}
	}
	// Truncated (openStack non-empty at end of string) — recover by closing
	// what's there. Terminate an open string literal first, then emit matching
	// closers in reverse open order. Worst case the result still doesn't parse
	// (we never make it worse than the raw tail); best case we salvage the N-1
	// complete questions and only lose the half-written trailing one.
	//
	// s[start:] 是字节切片,如果 MaxTokens 截断点恰好落在一个 3 字节中文汉字的中间
	// (切了 1-2 字节),out 起点就带半个 UTF-8 序列,后续 json.Unmarshal 会报
	// "invalid UTF-8" 而非真正的语法错误。strings.ToValidUTF8 把残缺字节剔除,
	// 保证喂给 Unmarshal 的是合法 UTF-8。
	out := strings.ToValidUTF8(s[start:], "")
	if inString {
		out += "\""
	}
	for i := len(openStack) - 1; i >= 0; i-- {
		if openStack[i] == '{' {
			out += "}"
		} else {
			out += "]"
		}
	}
	return out
}

// RepairBareQuotesInJSON 尝试修复 LLM 在 JSON string value 里写的未转义裸 ASCII
// 双引号(引语),把它们替换成中文「」(成对交替)。这是 parse 失败时的兜底修复,
// 由 ParseLLMJSON 在第一次 json.Unmarshal 失败时调用。
//
// 核心启发式:用状态机模拟 JSON parser,追踪 inString 状态。当在 string 内部遇到 "
// 时,判断它是"真字符串结束"还是"引语":
//   - 真结束:后面(跳过空白)紧跟的是 JSON 结构字符 , : } ]
//   - 引语:后面紧跟的是其它字符(典型:中文字符,如 "用"对应思想"比较" 里的两个 ")
//
// 引语 " 成对替换成「」,用一个布尔 toggle 决定当前 " 是开「还是闭」。
//
// 边界处理:
//   - \" (反斜杠转义)是合法的,不动(状态机识别反斜杠转义)。
//   - 完全合法的 JSON(无裸引号)输入,输出和输入完全相同(函数幂等,可重复调用;
//     ParseLLMJSON 据此判断"是否真的修过")。
//   - 修复不保证 100% 成功(复杂情况如单边引号、复杂嵌套引语),最终仍可能 parse
//     失败,调用方据此决定是否报错。
//
// 字节级扫描而非 rune 级:中文 UTF-8 是 3 字节(0xE0-0xEF 开头),多字节字符的后续
// 字节(0x80-0xBF)不会是 " (0x22),所以字节级扫描中文安全。
func RepairBareQuotesInJSON(s string) string {
	var out strings.Builder
	out.Grow(len(s) + 16) // 少量余量,替换通常不增减太多字节

	inString := false
	escape := false      // 上一个字符是 \(正在转义下一个字符)
	quoteOpen := true    // 引语 " 的开/闭 toggle:true 时下一个引语 " 替换成「,false 替换成」

	for i := 0; i < len(s); i++ {
		c := s[i]

		if escape {
			// 上一字节是 \,当前字节是被转义的(如 \" \\ \n),原样输出,清除 escape。
			out.WriteByte(c)
			escape = false
			continue
		}

		if c == '\\' && inString {
			// string 内的反斜杠:转义下一字节。原样输出 \,下一字节走 escape 分支。
			out.WriteByte(c)
			escape = true
			continue
		}

		if c == '"' {
			if !inString {
				// string 开始。
				inString = true
				out.WriteByte(c)
				continue
			}
			// 在 string 内遇到 "。判断是真结束还是引语。
			//
			// Pair-aware(2026-07-29):如果上一个引语 " 已开未闭合(quoteOpen==false),
			// 那么这个 " 一定是配对的闭合引语,不管它后面是不是结构字符——因为成对性
			// 是硬约束:开了「就该有」。这修复了"引语后跟逗号"的误判(如 summary 里
			// ...象棋级别"毕业",之后... 的 毕业" 后面是逗号,isStringTerminator 会误判
			// 成真字符串结束,导致引号配对错乱、后续结构全崩)。原逻辑只靠 isStringTerminator
			// 判断,在引语恰好出现在值末尾、后面跟逗号时失效(summary ep2 生产故障)。
			if !quoteOpen {
				// 已开引语未闭合 → 这个 " 是闭合引语。
				out.WriteString("」")
				quoteOpen = true
				continue
			}
			if isStringTerminator(s, i+1) {
				// 真结束。
				inString = false
				// 重置引语 toggle(每个 string 独立配对,避免上一个 string 的残留影响下一个)
				quoteOpen = true
				out.WriteByte(c)
				continue
			}
			// 引语裸双引号:替换成「(开)。
			out.WriteString("「")
			quoteOpen = false
			// 仍在 string 内,inString 保持 true
			continue
		}

		out.WriteByte(c)
	}

	return out.String()
}

// isStringTerminator 判断 string 内的 " 后面(从 s[i:] 开始)是否是合法的字符串结束上下文:
// 即跳过空白后,下一个字节是 JSON 结构字符 , : } ]。若是,这个 " 是真的字符串结束;
// 否则是引语(如 "用"对应思想"比较" 中间的 ")。
func isStringTerminator(s string, i int) bool {
	for ; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t', '\n', '\r':
			continue // 跳过空白
		case ',', ':', '}', ']':
			return true // 结构字符:说明前面的 " 是真结束
		default:
			return false // 其它字符(典型:中文):说明 " 是引语
		}
	}
	return true // 末尾:也算结束(字符串在文件末闭合)
}
