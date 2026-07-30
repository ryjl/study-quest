# LLM JSON 裸引号治理踩坑

> 全项目解析 LLM JSON 的统一 chokepoint 是 `internal/ai/jsonx` 的 `ParseLLMJSON`。
> CLAUDE.md「引号问题看采坑文档」指向本文件。任何新增的"解析 LLM 返回 JSON"的点
> 都必须走 `jsonx.ParseLLMJSON`,禁止各自手写 `json.Unmarshal`。

## 故障一句话

LLM 在 JSON 的 string value 里写**未转义的裸 ASCII 双引号**(引语),如:

```json
{"keypoints": ["象棋级别"毕业"的判断标准"]}
```

`json.Unmarshal` 在第一个裸 `"` 误判字符串结束,后面紧跟的中文字符成非法 token,
报 `invalid character 'X' after object key:value pair`(X 是中文 UTF-8 多字节字符
首字节,报错里显示成 `'å'`/`'æ'` 之类,极具迷惑性——**看着像编码问题,其实是裸引号**)。
整个 run 失败,白烧几万 token。

这个故障在 **summary / homework / quiz / polish** 反复出现,生产多次事故。

## 根因:token 级建模问题,CJK 更严重

裸引号不是模型"不小心",是 **token 级建模的固有缺陷**:模型生成 `象棋级别` 之后,
物理上吐了一个 `"` token,但它没意识到这在 JSON 结构里是字符串结束符。中文场景
尤其严重——中文里引号(「」""'')用于引语是高频用法,模型把自然语言的引语习惯
带进了它以为是纯文本的 string value。

所以这是**无法靠 prompt 完全消除**的(prompt 加"引号必须转义"是软约束,LLM 偶尔不听),
只能靠**结构化约束**或**事后修复**。

## 三层防御

### 第 1 层:源头根治(response_format json_schema strict)—— 当前不可用

OpenAI 的 Structured Outputs(`response_format:{type:"json_schema",strict:true}`)
走**约束解码**(grammar-based):解码时 grammar 在 string 内**物理上禁止**裸 `"`,
模型发不出违规 token。这是唯一能 100% 根治的方案。

**但本项目当前后端不支持。** 2026-07-29 探测结论:

| 入口 | model | json_object | json_schema strict |
|------|-------|-------------|--------------------|
| `api.ja.870314.xyz`(中转站) | llmy | ❌ 400 `unsupported_parameter` | ❌ 400 |
| `edge.v2ex.com`(真实后端) | llmy | ❌ 400 `unsupported_parameter` | ❌ 400 |
| `edge.v2ex.com` | coder (GLM-5.2) | ❌ 400 | ❌ 400 |
| `edge.v2ex.com` | coder-m3 (MiniMax-M3) | ❌ 400 | ❌ 400 |
| `edge.v2ex.com` | coder-ds4 (DeepSeek-V4-Pro) | ❌ 400 | ❌ 400 |

错误 `Parameter 'response_format' is not supported` / `param: response_format` 一字不差,
是**网关/服务层在参数校验阶段统一拒绝**,跟底层模型能力无关——无论后端是 GLM、
MiniMax 还是 DeepSeek,都没机会到约束解码那层就被挡掉。

**根治路径(未来切换)**:换成支持约束解码的后端(vLLM + xgrammar/guidance/outlines,
或原生 OpenAI gpt-4o+),在 `internal/ai/provider.go` 的 `ChatRequest` 加 `ResponseFormat`
字段、各生成点启用即可。代码结构已预留:`ParseLLMJSON` 的 repair 兜底届时继续保留
作为第二保险。探测用一次性脚本(见本文末"探测脚本"段,key 不落地)。

### 第 2 层:事后修复(ParseLLMJSON)—— 当前主防线 ✅

`internal/ai/jsonx.ParseLLMJSON(raw, &v) (repaired bool, err error)` 是全项目解析
LLM JSON 的**唯一入口**。兜底链:

```
ExtractJSONObject          // 剥 ```json 围栏、挖首个平衡 {…}、截断兜底
  → json.Unmarshal         // 正常路径,绝大多数命中
  → 失败则 RepairBareQuotesInJSON  // 裸引号 → 中文「」(成对交替)
  → 再 json.Unmarshal
```

- **ExtractJSONObject**:比 first/last-brace 切片强——追踪 `{`/`[` 开符号栈,尊重
  string 字面量,在首个平衡闭合处停止;走到末尾仍未平衡(被 MaxTokens 砍断)时,
  先闭合未完结 string、再按栈逆序补闭符号,救回前面 N-1 个完整项。
- **RepairBareQuotesInJSON**:状态机模拟 JSON parser,在 string 内遇 `"` 时判断
  "真结束"(后面跳过空白是 `,` `:` `}` `]`)还是"引语"(后面是中文字符);引语 `"`
  成对替换成「」,用 toggle 决定开「/闭」。**pair-aware**:已开引语未闭合时,下一个
  `"` 必当闭合(修复"引语后跟逗号"的误判)。

**已覆盖的解析点**(全部走 ParseLLMJSON):

| 解析点 | 文件 | 能力 |
|--------|------|------|
| `ParseHomeworkGeneration` | `agent/homework_parse.go` | homework 作业卷 |
| `parseSummaryJSON` | `agent/summarizer.go` | summary 课程总结 |
| `parseQuizGeneration` | `agent/quizzer.go` | quiz 出题 |
| `runSelfCheck`(`{pass,note}`) | `agent/quizzer.go` | quiz 自检 |
| `parsePolishJSON` | `polish/polish.go` | polish 字幕校对 |
| `parseSummaryForTools` | `agent/tools.go` | 工具读存储的 summary |
| `parseHeadlineOnly` | `agent/course_summary.go` | pre-seed 取 headline |

**未覆盖(刻意)**:`helpers.go` 的 `parseStringArg`/`parseIntArg`(解析 tool-call
arguments)。这些是轻量 best-effort(失败返回空/-1,工具报错后模型下一轮自我纠正),
收益小,保持现状。

### 第 3 层:prompt 强化 —— 辅助

各生成 prompt 加规则:输出 JSON 时,string value 里的引语用中文「」代替 ASCII 双引号。
软约束,降低裸引号频率(给第 2 层减负),不消除。

## repair 的局限(为什么不是 100%)

repair 本质是**猜**:裸 `"` 到底是字符串真结束还是引语?靠上下文(后面跟结构字符
还是中文字符)启发式判断。盲区:

- **成对引语**:能救(pair-aware 已改进)。`象棋级别"毕业"了` → `象棋级别「毕业」了` ✅
- **孤例裸引号**(单边,`他说"你好` 只开不闭):配对错乱,救不回 ❌
- **复杂嵌套引语**(多层 `"`):方向难判 ❌

这些场景信息已丢失,事后猜不出来,那次 run 会失败,靠 service 层重试兜底(重试可能
撞同样问题,但 LLM 有随机性,换次生成可能就规范了)。这是后端不支持 response_format
时能做的极限;**根治只能靠第 1 层换后端**。

## 真实事故案例

### summary ep2(2026-07,两次 run 全失败)

DB `ai_runs.response_text` 精确定位:keypoints 里写 `象棋级别"毕业"` 的裸引号。
报错 `'å'/'æ'` 是 UTF-8 多字节字符首字节被 parser 报错时显示——**看着像编码 bug,
实际是裸引号**。第一次转义没生效,重试又撞同样问题。加 `RepairBareQuotesInJSON`
兜底后救回。

**调试教训**:别被 `invalid character 'å'` 误导去查编码,先 grep response_text 里的
裸 ASCII 双引号。

## 常见误判

1. **"报 `'å'` 是编码问题"** —— 不是。是裸引号让 parser 在中文处报错,显示的是
   多字节字符首字节。查 response_text 里的裸 `"`。
2. **"polish 之前用 first/last-brace 切片够用"** —— 不够。截断/嵌套对象/尾部散文
   都会让 first/last-brace 切错。`ExtractJSONObject` 的平衡栈是必须的。
3. **"repair 函数幂等所以没修过"** —— 反过来用:合法 JSON 输入 repair 输出与输入
   逐字节相同,`ParseLLMJSON` 据此判断"是否真的修过"(repaired 标志)。

## 探测脚本(一次性,key 不落地)

探测中转站/后端是否支持 response_format。脚本不进 git,跑完即删。在生产机上从 DB
取 key 注入环境变量,key 不打印不落盘:

```bash
# 在生产机(192.168.8.4)上跑
export API_KEY=$(python3 -c "
import sqlite3, os
con = sqlite3.connect(os.path.expanduser('~/data/studyquest-data/studyquest.db'))
print(con.execute(\"SELECT api_key FROM ai_providers WHERE is_enabled=1 AND capability='chat' LIMIT 1\").fetchone()[0])
")
# 然后 curl 带 response_format:{type:"json_object"} 和 json_schema strict 各一次,
# 看 HTTP 状态码:200+合法JSON=支持;400 unsupported_parameter=不支持。
```

直连真实后端(不经中转站)用同样方法,换 base_url 和 model 即可。
