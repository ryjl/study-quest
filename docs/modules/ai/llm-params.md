# LLM 调用参数集中管理

> 技术文档，进 git。全项目 LLM 参数（max_tokens / temperature / max_steps / HTTP 超时）的
> **单一真相源说明**。改参数前先读本文，改完同步本文。

## 一句话

所有 LLM 参数定义在 **`backend/internal/ai/params.go`**（package `ai`）。各生成点引用这里的命名常量，禁止再散落魔法数字。改值只改 params.go 一个文件 + 重新部署。

## 为什么集中

改之前这些参数散落在 6+ 个文件（summarizer / homework / polish / quiz / advice / user_report / course_summary / admin 实战测试 / openai_compat），值靠各自硬编码，查起来得到处 grep，改一处容易漏改别处（如 summary 改了 homework 没跟上）。集中后一眼查全，改一处全局生效。

## 为什么是代码常量而非 DB 可配

这些参数调值不频繁（调对一次就稳定）。DB 可配反而引入 admin 误配风险——如 `max_steps` 调成 0 会让 agent 立刻失败，`max_tokens` 调太小会截断。需要运行时热调的只有 `polish_concurrency`（并发数，受中转站限流影响，已在 `settings` 表单独可配，见 subtitle-queue.md），其余都走代码常量。

## ⚠️ 关键认知：MaxTokens 设大不会浪费 token

这是最容易搞错的一点。**MaxTokens 是 completion 的上限，不是预留额度。** 模型生成完即返回（`finish_reason="stop"`），实际只消耗真实生成量的 token——设 16000 但只生成 3021，就只花 3021。

- **设大 = 防截断安全垫**：极端长内容不会中途被砍断。
- **设小 = 偶发长内容被截断** → JSON 在中途断裂 → parse 失败 → 整次 job 失败，白烧几万 prompt token。这个代价**远大于**多生成几百 token。

所以这些值**宁可偏大**。只在有确凿实证超限（实测 completion 真的超过了设定）时才提，不为"降冗余"而降。

## 参数清单

### Temperature

| 常量 | 值 | 说明 |
|------|----|----|
| `DefaultTemperature` | `0.0` | 全仓库统一。教育产品，所有任务都是 factual 抽取/出题/校对，要确定性不要创造性 |

### MaxTokens（completion 上限，按 capability 区分）

| 常量 | 值 | 用途 | 依据 |
|------|----|----|------|
| `MaxTokensSummary` | 16000 | 课程总结（结构最重） | 输出含 headline+sections+key_points+methods+common_mistakes+takeaway+SVG，截断会断 JSON。给足安全垫 |
| `MaxTokensHomework` | 16000 | 作业卷生成 | 和 summary 同级，整卷结构重 |
| `MaxTokensQuiz` | 10000 | quiz 出题 | 8-12 题 + 富文本 explanation，体积大 |
| `MaxTokensQuizSelfCheck` | 800 | quiz 自检判决 | 结构极简（`{pass,note}`） |
| `MaxTokensPolish` | 12000 | 字幕校对 | 每 chunk 150 cues 的 changes/glossary diff，体积大 |
| `MaxTokensAdvice` | 2500 | 学习建议 | 300-700 字 |
| `MaxTokensUserReport` | 3000 | 用户跨课程报告 | 比 advice 略长 |
| `MaxTokensCourseSummary` | 3000 | 课程级总结 | 300-700 字 |
| `MaxTokensProviderProbe` | 10000 | admin 实战测试 | 要复现业务负载 |
| `MaxTokensPing` | 5 | provider 连通性探测 | 只验证 round-trip |

### MaxSteps（agent ReAct loop 步数上限）

每步是一次完整 LLM 调用（含全历史 messages）。太低 → 任务没收集够信息就被强制结束（`ErrMaxSteps`）；太高 → 模型循环时白烧 token。跑满上限会触发 forced final call（`ToolChoice=none`）强制产出，仍空才报 `ErrMaxSteps`（偶发，见 `docs/pitfalls/backend.md`「quiz max steps」）。

| 常量 | 值 | 用途 |
|------|----|----|
| `MaxStepsQuiz` | 6 | get_info→search→mastery→输出，6 步够 |
| `MaxStepsAdvice` | 10 | 跨课程 mastery 数据量大，留余量 |
| `MaxStepsUserReport` | 10 | 全课程遍历 |
| `MaxStepsCourseSummary` | 8 | pre-seed 已喂 headline，1-3 次深入即可 |
| `MaxStepsSelfCheck` | 1 | 单次判决，无工具 |

注：`agent.go` 的 `defaultMaxSteps=6` 是 `AgentOpts` 零值时的兜底（非 quiz 专用），保留原位 + 注释指向 params.go。

### HTTP 超时 / job deadline

| 常量 | 值 | 说明 |
|------|----|----|
| `HTTPClientTimeout` | 120s | `OpenAICompatProvider` 的 client 级超时，单次请求硬墙 |
| `SummaryJobTimeout` | 5min | summary 单次尝试的 ctx deadline（正常 30-40s，5min 给足 + 兜底 hang） |

## 不在 params.go 的（刻意保留原位）

- **polish 自适应 deadline**（`max(chunk数×5min, 20min)`）、`maxRetries=2`、指数退避（`2<<attempt` 秒）：与断点续润/并发强耦合，保留在 `internal/ai/polish/polish.go`。params.go 有注释指向。
- **`defaultMaxSteps`**（agent.go）：`AgentOpts` 零值兜底，保留 + 注释指向 params.go。

## Tuning 历史

| 参数 | 变更 | 依据 |
|------|------|------|
| `MaxTokensPolish` | 8000 → 12000 | polish completion 偶尔超 8000（中转站不严格遵守 maxTokens），提到 12000 留余量 |
| 集中化 | 散落字面量 → params.go 常量 | 单一真相源，消除"改一处漏一处"风险 |

## 怎么改参数

1. 改 `internal/ai/params.go` 里对应常量的值（+ 更新注释里的依据）。
2. 同步本文「参数清单」表格 + 「Tuning 历史」。
3. `make deploy`（代码常量改动需重新部署生效）。
4. 若改的是 MaxTokens，验证目标 job 的 ai_runs.completion_tokens 不再被截断。
