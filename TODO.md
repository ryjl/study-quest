# TODO — StudyQuest 待办清单

> 本文件只记录**近期计划做**的 feature idea 和技术债,按优先级分组。
> 已完成的功能不在此记录(git log 是完成历史)。长远/已搁置的方向见
> [`docs/ROADMAP.md`](docs/ROADMAP.md)。
>
> 每条尽量写清:**场景**(解决什么用户问题)、**价值**、**工作量**(小/中/大)、
> **依赖**(前置条件)。

---

## P0 — 高价值,建议优先做

### memory 衰减曲线(艾宾浩斯)

`KnowledgeMemory.mastery` 当前是单调累积(答对 +0.1 / 答错 -0.2),不随时间衰减。加
艾宾浩斯遗忘曲线,让长时间没复习的知识点 mastery 自然回落,触发 agent 重新出题巩固。

- **场景**:学生一个月前答对的"通分"现在可能已经忘了,但 mastery 还是 0.9,agent
  不会再出这题。衰减让"过时掌握"自动浮现。
- **价值**:让系统更接近真实学习规律,自适应更准——直接提升 advice 质量 + 考试抽题
  能抓住"以为掌握了其实忘了"的弱点。
- **工作量**:小到中。`KnowledgeMemory` 加 `decay_*` 列(加列,零风险),读时算衰减、
  不需要后台 cron。`LastReviewed` 字段已存在可直接用。
- **依赖**:无。

### AI 输出质量仪表盘

`QuizSelfCheck` 已经在跑(出题后第二轮 LLM 审题,pass/fail/regenerated 写
`AIRun.SelfCheckResult`),但没有聚合视图。

- **场景**:admin 想发现"某个学科的 quiz fail-rate 突然升高"或"换模型后质量下降",
  现在只能逐条 run 看。
- **价值**:质量治理。能及时发现 prompt/模型版本的输出质量回归。数据全在 `ai_runs` 表
  (self_check_result + capability + model_used),杠杆在 UI 不在数据采集。polish 也写
  `ai_runs`(capability="polish"),仪表盘能覆盖全部 AI 能力。
- **工作量**:中。按学科/题型/self-check 结果分组的 fail-rate 趋势图。
- **依赖**:无。

---

## P1 — 中等价值

### 作业卷 self-check 二次校验(实测驱动)

作业卷当前是单次 LLM 调用 + 残题丢弃,没有 quiz 那种 9 维度 self-check(答案正确性、
干扰项合理性等)。

- **场景**:如果实测发现 AI 出的作业偶有"答案错""干扰项太离谱"等问题,加 self-check 能拦住。
- **价值**:质量保险。每份作业 +1 次小 LLM 调用校验,fail 时标 `AIRun.SelfCheckResult=fail`
  + admin 页红色标记,可单独"重出此题"。
- **触发条件**:**先实测当前生成质量再决定要不要做**。质量够好则不做(做了只是多烧
  token + 多一层 UI 复杂度)。
- **工作量**:中。复用 quiz self-check 范式,`SelfCheckResult`/`SelfCheckNote` 字段已存在
  于 `AIRun` 表(无需迁移)。
- **依赖**:无。

### admin 批量预热出题

`EnqueueQuiz` 结构已预留,未来 admin 可一键给某课程所有 episode 批量预热出题。

- **场景**:新课程上线时,第一批学生进 AI 页都要等几十秒生成。批量预热让首屏 ready。
- **价值**:体验提升,但和"省钱"原则有张力(预热会跑没人看的题)。建议做成 admin 显式
  触发,不默认开。
- **工作量**:小。结构已预留,接路由 + admin UI 按钮即可。
- **依赖**:无。

---

## 技术债

### AIConfig 扩展新配置项(JSON 化的收益兑现)

`Course.AIConfigJSON` / `Subject.AIConfigJSON` 单 JSON 列设计成前向兼容——加新配置项
不必改 schema。出现新需求时,扩 `model.AIConfig` struct 加字段 → admin 表单加输入 →
service 层 `SetAIConfig` 自然带上。候选:难度系数、题型配比、语言偏好、禁用术语纠错。
都是代码-only 改动,零 schema 迁移。

### 多选题 mastery 加权数值微调

当前 multi_choice grading:全对 +0.1 / 部分对(漏选)按错处理 -0.2 / 错 -0.2。部分对一律
-0.2 是粗糙折中(漏 1 个和勾中 3 个里的 2 个同等扣分)。可加 partial 参数,让部分对按
"勾中正确项数 / 正确项总数"给比例分。

- **工作量**:小。`RecordAnswer` 签名扩 partial/Score + 增量公式调 + 单测调阈值。
- **触发条件**:多选题上线后观察一段时间,确认加权不会让 mastery 抖动过大。

### advice 重算策略细化

当前 `UpsertAdvice` 每次交卷都覆盖(episode 级 advice 在 submit-all 后链式触发重算)。
学生一节课做 5 次 quiz,advice 重算 5 次,每次烧 token 但内容可能没实质变化。

- **要做什么**:加节流——"上次建议后答了 N 题才重算"或"mastery 变化超过阈值才重算"。
- **工作量**:小。service 层加重算前检查(对比 `MasterySnapshotJSON` 和当前 mastery 的 diff)。
- **触发条件**:观察到 advice 重算频率过高 / token 账单上涨。
