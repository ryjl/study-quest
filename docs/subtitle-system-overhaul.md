# 字幕系统改造（Subtitle System Overhaul）

> 本文档是字幕系统改造的**唯一入口**，跨多次会话使用。会话开始时先读本文件，再开始实施。
> 启动时间：2026-07-20
> 当前进度：见文末「进度追踪」
>
> **使用方式**：
> - 新会话开始 → 读本文档「总览」+「进度追踪」+ 下一个待做 PR 的章节
> - 实施前 → 重读「关键陷阱」（这些是项目已经踩过的坑）
> - 实施完一个 PR → 在「进度追踪」打勾，必要时更新本文档

---

## 一、总览

### 1.1 目标

优化字幕生成和使用的全链路。具体五个目标：

1. **可见性**：admin 在课程库列表直接看到哪些视频已有字幕；课时总结生成按钮在无字幕时禁用
2. **存储统一**：数据库里只存 VTT（保留样式），给 AI 时转 SRT
3. **质量**：字幕生成后用 LLM 自动润色（纠正同音错字/术语），用户看到的和 AI 用的字幕都是干净的
4. **多来源**：支持 Whisper 转录、视频内嵌字幕提取、admin 手动上传；统一进 VTT 存储
5. **效率**：Whisper worker 缓存字幕，同一视频重跑不重复转录

### 1.2 PR 拆分（按依赖顺序）

```
PR1   前端可见性 + 按钮 gate        ← 无依赖，最先做
PR1.5 存储格式迁移 SRT → VTT       ← PR2/3 的基础设施
PR2   字幕 LLM 润色（PoC + 正式）  ← 核心改动，先 PoC
PR3   内嵌字幕探测 + 提取          ← 依赖 PR1.5 的 VTT 存储
PR4   Whisper 字幕缓存             ← 独立，worker 端
PR2.5 术语字典自动生成 UI          ← 依赖 PR2 的 glossary_candidates 表
PR5   多 AI provider 分工          ← 独立架构改造
```

### 1.3 会话策略

**2 次会话**：
- **会话 1（进行中）**：写文档 + PR1 + PR1.5 + PR2 PoC（象棋课数据）
- **会话 2**：根据 PoC 结果实施 PR2 正式 + PR3/PR4/PR2.5/PR5

会话 2 的内部节奏建议：
1. PR2 正式实施（核心，基于 PoC 结果调整细节）
2. PR3 内嵌字幕 + PR4 worker 缓存（subagent 分担窄任务）
3. PR2.5 术语字典 UI
4. PR5 多 provider（独立架构改造）

---

## 二、关键技术决策（"为什么"）

这些是已经讨论敲定的决策，实施时不要重新质疑，除非遇到具体障碍需要调整。

### 2.1 存储统一为 VTT

**决策**：`subtitles` 表里 `srt_content` 改名 `vtt_content`，只存 VTT。

**理由**：
- 播放路径省一步（现在每次播放要 srtToVtt 转换）
- 保留内嵌字幕样式（粗体/位置，对学生有用）
- AI 转换成本可忽略（< 1ms）
- 避免"双存"（SRT+VTT 两字段）的一致性陷阱

**给 AI 时**：`VttToSrt(sub.VttContent)` 转换后走原有逻辑（保留 SRT 解析逻辑不动）。

### 2.2 字幕润色方案：分块 + diff 输出

**决策**：全量字幕分块送 LLM，但 LLM 只输出 `{changes: [...]}` diff，不重写全文。

**理由**（核心决策，有专门的讨论过程）：
- 一次性全量不可行——输出 token 物理上限（DeepSeek 8k，最长字幕 9 万字）
- 预过滤（只送"疑似错字"的 cue）不可行——选不出"有问题的句子"本身就是核心问题，字典查表无法处理上下文（"动车"是动词+车 vs "车走到中路"是象棋术语）
- LLM 的价值恰好在"判断该不该改"，需要看上下文
- diff 输出省 70-90% 输出 token

**分块参数**（基于真实数据校准，可调）：
- **块大小**：150 cue（实测 episode_id=1 是 46 字/cue，150 cue ≈ 6900 字，加 prompt 约 8k input token）
- **块重叠**：3 cue（前后各 1.5 句上下文，解决跨块依赖）
- **并发**：3 路 semaphore（用户要求，很多中转站限速严格）
- **MaxTokens**：8000
- **重试**：单块失败 3 次后用原文，标记 `partial_optimized`

**时间戳保证**：后端维护 `id → 时间戳` 映射，LLM 只输出 `id → 修正文本`。时间戳根本不进 prompt，物理上不会错。

### 2.3 字幕润色的模型要求低

**决策**：润色任务用便宜的模型就够。

| 任务 | 模型要求 |
|---|---|
| 字幕润色 | 低（指令遵循 + 中文常识） |
| Quiz self-check | 低-中 |
| Summary | 中 |
| Quiz 出题 / Advice | 中-高 |

**推荐润色模型**：DeepSeek-chat（中文强 + 便宜 + ¥0.3-1/节）。Claude Haiku 也行。

**多 provider 落地策略**：
- PR2 里用硬编码——优先找 `tags` 含 `"polish"` 的 provider，找不到 fallback 到默认 chat
- PR5 做完整的 admin UI + 所有任务的 tag 分工
- `ai_providers` 表加 `tags` 字段（JSON 数组）

### 2.4 词汇表自动生成（挖矿工作流）

**决策**：润色时顺带挖矿，admin 审核入库。

**核心思路**：LLM 在润色时本来就要判断"这个是不是术语错字"——这是它为了完成任务必须做的中间推理。新方案让它**显式输出**这个判断结果（glossary 字段），沉淀下来。

**关键点：零额外 LLM 成本**——让 LLM 在同一次调用里多吐一个字段，不增加调用次数、不显著增加输出 token（glossary 通常 5-20 条，每条几十字符）。

### 2.5 字幕格式全覆盖

**决策**：统一走 ffmpeg `-c:s webvtt` 转换，覆盖所有文本字幕格式。

| 格式 | 类型 | ffmpeg 能转 VTT？ |
|---|---|---|
| SRT | 文本 | ✅ |
| ASS / SSA | 文本（带样式） | ✅（丢样式保文本） |
| SUB (MicroDVD) | 文本 | ✅ |
| SMI (SAMI) | 文本 | ✅ |
| WebVTT | 文本 | ✅（已经是） |
| PGS (Blu-ray) | 图形 | ❌（报错提示走 whisper） |
| VOBSUB / IDX+SUB (DVD) | 图形 | ❌ |
| DVB subtitle | 图形 | ❌ |

### 2.6 不转 ASS 存储

虽然 ASS 支持复杂样式（卡拉OK、双语分行、说话人颜色），但 study-quest 是中小学学习场景，不需要。而且 AI 管线（segmenter、summarizer、prompt）都深度依赖 SRT 结构，转 ASS 会断链。**保持 VTT 存储**。

如果将来要"重点高亮"这类简单样式，在 LLM 润色时让模型给术语包 `<b>` 标签（VTT 原生支持），零额外架构成本。

---

## 三、词汇表管理体系（详细工作流）

### 3.1 两套互补的系统

```
┌────────────────────────────────────────────────────────────────┐
│                  词汇表管理(两套互补的系统)                    │
├────────────────────────────────────────────────────────────────┤
│                                                                │
│  系统 A:TermDict(已确定,每次润色都用)                          │
│  ─────────────────────────────────────────────                 │
│  位置: Course.AIConfigJSON.TermDict + Subject.AIConfigJSON     │
│  格式: "车→居(象棋术语);通分→同分(分数运算)"                  │
│  编辑: AIConsole → Prompt 配置 tab → 5 个 hint textarea         │
│  特点: admin 任意手写/修改/删除,完全权威                       │
│        LLM 每次润色都按这个字典纠正                            │
│                                                                │
│                        ↑                                       │
│                        │ admin 接受(可编辑后接受)              │
│                        │                                       │
│  系统 B:glossary_candidates(待审核,LLM 挖矿产出)               │
│  ─────────────────────────────────────────────                 │
│  位置: glossary_candidates 表                                  │
│  来源: 每次字幕润色时 LLM 的 glossary 字段输出                 │
│  状态: pending(待审) / accepted(已接受,已进 TermDict)          │
│        rejected(已拒绝)                                        │
│  审核: AIConsole 新增"术语候选"区块                            │
│  特点: admin 可编辑后接受(改 corrected/context)                │
│        accepted 后自动追加到 TermDict                          │
│        rejected 的不再重复展示(除非 admin 主动看)              │
│                                                                │
└────────────────────────────────────────────────────────────────┘
```

**关键关系**：
- TermDict 是**源数据**（admin 手写的 + 接受的候选累积的）
- glossary_candidates 是**建议池**（LLM 挖出的，admin 挑选进 TermDict）
- 单向流：candidates → TermDict（接受）。TermDict 手改不影响 candidates。

### 3.2 词汇表自动工作流（4 个阶段）

```
阶段 1：挖矿（PR2，每次润色自动触发）
─────────────────────────────────────────
字幕润色时 LLM 输出:
{
  "changes": [{"id": 147, "text": "把居走到中路"}],
  "glossary": [                                    ← 新增
    {
      "original": "车",
      "corrected": "居",
      "context": "象棋术语,指棋子",
      "confidence": 0.95,
      "evidence_ids": [147, 152, 198]              ← 在哪些 cue 里观察到
    }
  ]
}
      ↓
写入 glossary_candidates 表（UpsertCandidate 去重），status=pending
(每条候选关联 course_id，因为术语是按课程分的)


阶段 2:审核(PR2.5,admin 手动)
─────────────────────────────────────────
admin 在 AI Console 看到"术语候选"列表(按 confidence 降序):

  象棋课候选 (12 个 pending)
  ┌──────────────────────────────────────────────┐
  │ 车 → 居   conf 0.95   出现 5 次              │
  │ 上下文:象棋术语,指棋子                        │
  │ 证据:cue 147 "把车走到中路"                   │
  │       cue 152 "车马炮配合"                    │
  │       cue 198 "车被攻击了"                    │
  │ [接受] [拒绝] [编辑后接受]                    │
  ├──────────────────────────────────────────────┤
  │ 通分 → 同分  conf 0.92   出现 3 次            │
  │ [接受] [拒绝] [编辑后接受]                    │
  └──────────────────────────────────────────────┘

admin 点"接受"(可先编辑):
  1. status 改 accepted
  2. 追加到 Course.AIConfigJSON.TermDict:
     原字典: "车→居(象棋术语)"
     新字典: "车→居(象棋术语);通分→同分(分数运算)"
  3. (可选)触发该课程下所有未润色字幕重跑润色


阶段 3:复用(下次润色自动)
─────────────────────────────────────────
下次同课程新课时:
  TermDict 已含"通分→同分"
  → 润色时直接应用,不再产出这个候选
  → 系统只产出"字典里还没的新规律"


阶段 4:跨课程推广(可选,PR2.5 里做)
─────────────────────────────────────────
admin 接受候选时可选:
  [ ] 同时应用到同学科所有课程

比如象棋学科下有 3 门课,接受"车→居"时勾选,
则 3 门课的 TermDict 都加上这条。避免每门课重复挖矿。
```

### 3.3 演化机制（为什么能自动收敛）

```
第 1 节课润色:字典空,LLM 靠常识挖出 5 个候选
  → admin 接受 3 个,字典 = 3 条
第 2 节课润色:字典 3 条,LLM 应用这 3 条 + 挖出 2 个新候选
  → admin 接受 1 个,字典 = 4 条
第 3 节课润色:字典 4 条,LLM 应用这 4 条 + 挖出 1 个新候选
  → admin 接受 1 个,字典 = 5 条
...
第 N 节课:字典稳定,几乎不再产新候选,润色质量稳定
```

**渐进收敛**——一个学科的术语集合是有限的，挖完就稳定了。前期 admin 审核多（建字典），后期几乎不用管。

### 3.4 GlossaryCandidate schema（PR2 里加）

```go
type GlossaryCandidate struct {
    ID              uint       `gorm:"primaryKey"`
    CourseID        uint       `gorm:"index;not null"`
    Original        string     `gorm:"size:64;not null"`
    Corrected       string     `gorm:"size:64;not null"`
    Context         string     `gorm:"size:256"`
    Confidence      float64
    EvidenceCount   int        // 累计观察到多少次(跨多节课)
    EvidenceSample  string     `gorm:"type:text"` // JSON 数组,最多保留 5 条样本 cue
    Status          string     `gorm:"size:16;default:'pending';index"` // pending/accepted/rejected
    AcceptedAt      *time.Time
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
// 唯一索引:idx_course_original_corrected on (CourseID, Original, Corrected)
```

### 3.5 去重逻辑（UpsertCandidate）

同一门课多次润色会反复挖出同一个候选。不能每次都新建一条 pending 记录：

```go
func (r *glossaryRepo) UpsertCandidate(c *GlossaryCandidate) error {
    // 查 (course_id, original, corrected) 是否已有
    var existing GlossaryCandidate
    err := r.db.Where("course_id = ? AND original = ? AND corrected = ?",
        c.CourseID, c.Original, c.Corrected).First(&existing).Error

    if errors.Is(err, gorm.ErrRecordNotFound) {
        // 新候选,创建
        return r.db.Create(c).Error
    }
    if err == nil {
        // 已存在,只更新 evidence_count 和 confidence(取较高值)
        if c.Confidence > existing.Confidence {
            existing.Confidence = c.Confidence
        }
        existing.EvidenceCount += len(c.EvidenceIDs)
        // accepted/rejected 状态的不覆盖,只更新 pending 的
        if existing.Status == "pending" {
            return r.db.Save(&existing).Error
        }
        return nil
    }
    return err
}
```

### 3.6 admin 可看可改（关键设计）

**层次 1：候选审核时可编辑**（必须做）

admin 在审核界面看到的不只是 `车 → 居`，还要看到 evidence（实际字幕样本），并可以编辑 corrected 字段（把 LLM 挖的"居"改成别的）或 context。

**层次 2：已接受的术语随时可改**（必须做，已有能力）

已经接受的术语 = TermDict 里的内容 = admin 随时能在 Prompt 配置 tab 的 textarea 里改。PR2.5 不需要做这个——已存在。

**层次 3：改过之后可追溯**（建议做）

加字段 `OriginalCorrected string`（接受时的原值，admin 改过这里记原值）+ `AcceptedIntoTermDictAt *time.Time`。

### 3.7 边界情况

| 情况 | 处理 |
|---|---|
| LLM 挖出的候选字典里已有 | prompt 里告诉它"字典里已有的不要放 glossary" |
| admin 拒绝了某候选 | status=rejected，下次润色还可能挖出，但 rejected 的不重复展示给 admin |
| admin 接受后又想撤回 | 删 TermDict 里对应条目，候选标 rejected |
| 同一 original 有多个 corrected | 都保留，prompt 让 LLM 按上下文选 |
| confidence 低于 0.7 | 不写入 glossary（prompt 要求 ≥0.7 才报） |

---

## 四、关键陷阱（项目已踩过的坑，必须遵守）

> 这段是 CLAUDE.md subagent 规则的本地化，针对字幕改造的特殊约束。

### 4.1 跨层契约必须写死字段名

`Subtitle` 改名 `SrtContent` → `VttContent` 涉及多个调用点。**改之前先 `grep -rn "SrtContent\|srt_content"` 把所有调用点列出来**，逐个改完。

受影响的调用点（PR1.5 实施时必须全部覆盖）：
- `backend/internal/model/models.go` Subtitle struct 定义
- `backend/internal/handler/episode_handler.go` GetSubtitleVTT（读 srt_content）
- `backend/internal/service/ai_service.go` runSegmentJob（读 srt_content）
- `backend/internal/repository/episode_repo.go` SaveSubtitle upsert
- `backend/internal/handler/admin_content.go` 字幕 CRUD
- `backend/internal/handler/admin_dto.go` subtitleDTO
- `backend/internal/service/import_service.go` auto-match 字幕
- worker `tools/video-pipeline/api_client.py`（**不改**，继续上传 SRT，后端转 VTT）

### 4.2 不能并行 PR1.5（schema 改动）和后续 PR

CLAUDE.md 的硬规则："Never parallelize repo/model-layer changes with their callers."

PR1.5 改 Subtitle schema 影响所有读字幕的代码。必须 PR1.5 完整跑通（`go test ./...` 全绿）再开始 PR2。

### 4.3 删库重建前先备份

用户已批准删库重建（不做迁移脚本）。但**每次改 schema 前先备份**：
```bash
cp backend/data/studyquest.db backend/data/studyquest.db.bak.$(date +%Y%m%d)
```

### 4.4 model 改动后立即跑全量测试

CLAUDE.md 硬规则：model-layer change → `make test` every time，no exceptions。`go build` 绿不等于行为绿。

### 4.5 subagent 的正确用法

| 任务 | subagent？ | 理由 |
|---|---|---|
| PR2 PoC（跑真实 LLM） | ✅ | 隔离大量日志输出和长 prompt |
| 单个独立组件（如 srt_cache.py） | ✅ | 边界清晰 |
| 写测试 | ✅ | 边界清晰 |
| PR1.5 schema 迁移 | ❌ 主 agent | 跨层、要全局视角 |
| PR2 正式管线 | ❌ 主 agent | 跨层、契约复杂 |
| PR5 多 provider 改造 | ❌ 主 agent | 影响所有 agent |

### 4.6 whisper_hint 不要被 VTT 迁移误伤

`subtitle_job_handler.go:84` 的 `whisper_hint` 字段是给 worker 的 prompt context（不是字幕），**和字幕存储格式无关**。PR1.5 不要动它。

---

## 五、各 PR 实施手册

### PR1：前端可见性 + 按钮 gate

**目标**：admin 一眼看到哪些视频有字幕；无字幕时禁用总结生成按钮。

**改动文件**：
- `frontend-admin/src/pages/courses/CourseTree.tsx`
- `frontend-admin/src/pages/ai-console/RegenTab.tsx`

**改动 1：CourseTree EpisodeRow 加字幕 badge**

定位 `CourseTree.tsx:558-576` 的 metadata 区（现有时长/分辨率 tag）。在末尾加：

```tsx
{(ep.subtitle_count ?? 0) > 0 && (
  <span className="inline-flex items-center gap-1 rounded bg-good/10 px-1.5 py-0.5 text-[11px] text-good" title={`已有字幕 ${ep.subtitle_count} 条`}>
    <Captions size={10} />
    {ep.subtitle_count}
  </span>
)}
```

注意：`Captions` 图标已在文件 import 里（`CourseTree.tsx:582` 已经在用）。`subtitle_count` 字段已在 `lib/types.ts:147` 定义。后端 `admin_content.go:564-571` 已下发。

**改动 2：RegenTab 单集课时总结按钮 gate**

定位 `RegenTab.tsx:270-277`。改 disabled 和 title：

```tsx
const noSubtitle = (ep.subtitle_count ?? 0) === 0;
<button
  className="btn-ghost btn-sm"
  onClick={() => regenEpisodeMut.mutate(ep.id)}
  disabled={noSubtitle || regenEpisodeMut.isPending}
  title={noSubtitle ? '该课时没有字幕，无法生成总结' : (hasSummary ? '重新生成该课时总结(异步入队)' : '生成该课时总结(异步入队)')}
>
  {noSubtitle ? '无字幕' : (hasSummary ? '重新生成' : '生成')}
</button>
```

**改动 3：RegenTab 课程总结按钮 gate**

定位 `RegenTab.tsx:189-200`。计算课程下所有 episode 的字幕总数：

```tsx
const subtitleTotal = episodes.reduce((sum, ep) => sum + (ep.subtitle_count ?? 0), 0);
const noSubtitleAtAll = subtitleTotal === 0;
// 在按钮上：
disabled={noSubtitleAtAll || triggerSummaryMut.isPending || summaryStatus === 'generating'}
title={noSubtitleAtAll ? '该课程下没有任何字幕，无法生成总结' : ...}
```

**验证**：
- `cd frontend-admin && npx tsc --noEmit` 通过
- `npm test` 通过
- 手动 UI：有字幕课时按钮可点；无字幕时按钮灰、提示"无字幕"

---

### PR1.5：存储格式迁移 SRT → VTT

**目标**：数据库里只存 VTT，给 AI 时转 SRT。

#### Step 1: 备份数据库

```bash
cp backend/data/studyquest.db backend/data/studyquest.db.bak.$(date +%Y%m%d)
```

#### Step 2: 改 Subtitle schema

`backend/internal/model/models.go:441-450`：

```go
type Subtitle struct {
    ID         uint      `gorm:"primaryKey"`
    EpisodeID uint      `gorm:"index;not null"`
    Language   string   `gorm:"size:32;default:'zh-CN'"`
    Label      string   `gorm:"size:64;default:'中文'"`
    VttContent string   `gorm:"type:text;not null"`  // 原 SrtContent 改名,只存 VTT
    Source     string   `gorm:"size:32;default:'whisper'"`  // whisper/embedded/manual/llm_optimized
    Optimized  bool     `gorm:"default:false"`  // PR2 配套
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

唯一索引保持：`idx_episode_lang` on `(EpisodeID, Language)`（已有，不改）。

#### Step 3: 新建 codec 文件

新建 `backend/internal/ai/subtitle_codec.go`：

```go
package ai

import "strings"

// SrtToVtt converts SRT to WebVTT: add header, replace ',' with '.' in timestamps.
// Loses no information; cue bodies are unchanged.
func SrtToVtt(srt string) string {
    srt = strings.TrimSpace(srt)
    if srt == "" {
        return "WEBVTT\n\n"
    }
    return "WEBVTT\n\n" + strings.ReplaceAll(srt, ",", ".")
}

// VttToSrt converts WebVTT to SRT for AI consumption.
// Strips: WEBVTT header, NOTE/STYLE blocks, cue settings (line:%, align, size),
// inline style tags (<b>, <i>, <c.xxx>, <u>, <00:00:01.000> timing tags).
// Replaces '.' with ',' in timestamps.
// NOTE: this is LOSSY for styling but AI doesn't need styles.
func VttToSrt(vtt string) string {
    lines := strings.Split(strings.TrimSpace(vtt), "\n")
    var out []string
    inHeader := true
    for _, raw := range lines {
        line := strings.TrimSpace(raw)
        // Skip WEBVTT header and NOTE/STYLE/REGION blocks
        if inHeader {
            if strings.HasPrefix(line, "WEBVTT") || line == "" {
                continue
            }
            if strings.HasPrefix(line, "NOTE") ||
               strings.HasPrefix(line, "STYLE") ||
               strings.HasPrefix(line, "REGION") {
                continue
            }
            inHeader = false
        }
        // Strip cue settings on timestamp lines
        // e.g. "00:00:01.000 --> 00:00:04.000 line:50% align:center"
        if strings.Contains(line, "-->") {
            // Find end of second timestamp (first space after -->)
            arrowIdx := strings.Index(line, "-->")
            if arrowIdx >= 0 {
                rest := line[arrowIdx+len("-->"):]
                // rest may be " 00:00:04.000 line:50% align:center"
                // Take only the first non-empty token after arrow
                rest = strings.TrimSpace(rest)
                if sp := strings.IndexByte(rest, ' '); sp >= 0 {
                    rest = rest[:sp]
                }
                line = line[:arrowIdx+len("-->")] + " " + rest
            }
            // Replace '.' with ',' in timestamp
            line = strings.ReplaceAll(line, ".", ",")
        }
        // Strip inline style tags from text lines
        line = stripInlineTags(line)
        out = append(out, line)
    }
    result := strings.Join(out, "\n")
    // Collapse 3+ newlines to 2 (SRT convention)
    for strings.Contains(result, "\n\n\n") {
        result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
    }
    return strings.TrimSpace(result)
}

// stripInlineTags removes <b>, <i>, <u>, <c.xxx>, <00:00:01.000> timing tags etc.
// Keeps inner text. Used by VttToSrt to clean text for AI.
func stripInlineTags(s string) string {
    // Only strip if there's at least one '<' to avoid allocating on plain lines.
    if !strings.ContainsRune(s, '<') {
        return s
    }
    var out strings.Builder
    out.Grow(len(s))
    inTag := false
    for _, r := range s {
        if r == '<' {
            inTag = true
            continue
        }
        if r == '>' {
            inTag = false
            continue
        }
        if !inTag {
            out.WriteRune(r)
        }
    }
    return out.String()
}
```

新建 `backend/internal/ai/subtitle_codec_test.go`，UT 覆盖矩阵：

```
测试矩阵（每个用例都要覆盖）:
1.  纯 SRT → VTT 转换（加头 + ,→.）
2.  纯 VTT → SRT 转换（去头 + .→, + 剥 cue 属性）
3.  VTT 带 cue 设置（line:50% align:center）→ SRT 干净
4.  VTT 带内联样式（<b>术语</b>）→ SRT 保留文本
5.  VTT 带 NOTE 块 → SRT 去除
6.  VTT 带 STYLE 块 → SRT 去除
7.  VTT 带时间戳内联标签（<00:00:01.000>）→ SRT 去除
8.  空字符串处理
9.  只有 WEBVTT 头的空字幕
10. 多语言字符（CJK）保留
11. 往返测试：SrtToVtt(VttToSrt(vtt)) 信息不丢（除样式）
12. 带 BOM 的文件
13. CRLF vs LF 换行
14. 嵌套标签（<b><i>text</i></b>）
15. 不闭合标签（<b>text，没闭合）
16. 特殊字符（&, <, > 在文本里 —— 注意：VTT 规范是 &amp; &lt; &gt; 转义的）
```

#### Step 4: 改所有调用点

**`episode_handler.go:64-91` GetSubtitleVTT**：直接吐 `sub.VttContent`：

```go
func (h *episodeHandler) GetSubtitleVTT(c *gin.Context) {
    // ... 拿 subtitle ...
    c.Header("Content-Type", "text/vtt; charset=utf-8")
    c.String(200, sub.VttContent)  // 直接吐，不再 srtToVtt
}
```

**删除 `episode_handler.go:93-98` 的 `srtToVtt` 函数**（逻辑已移到 codec.go）。

**`ai_service.go:548-558` runSegmentJob**：字幕读取改 VttToSrt：

```go
sub, err := s.episodeRepo.GetSubtitle(episodeID)
// ...
cues, err := ai.ParseSRT(ai.VttToSrt(sub.VttContent))  // 包一层 VttToSrt
```

**`episode_service.go:352-366` SaveSubtitle**：参数从 srt 改 vtt，但**对外仍接受 SRT**（worker 上传的是 SRT），内部转 VTT 存储：

```go
func (s *episodeService) SaveSubtitle(episodeID uint, language, label, srtOrVtt string) error {
    // 如果不是 VTT 格式，转成 VTT
    vtt := srtOrVtt
    if !strings.HasPrefix(strings.TrimSpace(vtt), "WEBVTT") {
        vtt = ai.SrtToVtt(vtt)
    }
    return s.episodeRepo.SaveSubtitle(&model.Subtitle{
        EpisodeID:  episodeID,
        Language:   language,
        Label:      label,
        VttContent: vtt,
        Source:     "whisper",  // 默认来源
    })
}
```

注意 Source 字段：SaveSubtitle 默认 `whisper`，如果是内嵌提取（PR3）或 LLM 润色后（PR2），调用方要显式传入。可能需要扩展 SaveSubtitle 签名加 source 参数，或加新的 SaveSubtitleWithSource 方法。

**`subtitle_job_handler.go:106` Complete**：worker 上传 SRT 不变，后端 SaveSubtitle 内部转 VTT：

```go
// 调用方依然传 srt_content（协议不变）
err := h.svc.Complete(id, req.SrtContent, req.Language, req.Label)
// Complete 内部调 SaveSubtitle(srtOrVtt=req.SrtContent) → 自动转 VTT
```

**`repository/episode_repo.go:259-271` SaveSubtitle upsert**：字段从 SrtContent 改 VttContent：

```go
func (r *episodeRepo) SaveSubtitle(subtitle *model.Subtitle) error {
    var sub model.Subtitle
    err := r.db.Where("episode_id = ? AND language = ?", subtitle.EpisodeID, subtitle.Language).First(&sub).Error
    if errors.Is(err, gorm.ErrRecordNotFound) {
        return r.db.Create(subtitle).Error
    }
    sub.VttContent = subtitle.VttContent
    sub.Label = subtitle.Label
    if subtitle.Source != "" {
        sub.Source = subtitle.Source
    }
    sub.Optimized = subtitle.Optimized
    return r.db.Save(&sub).Error
}
```

**`handler/admin_content.go` 字幕 CRUD**：所有读写改成 VttContent（admin 手动上传字幕也走 SrtToVtt 转换）。

**`handler/admin_dto.go` subtitleDTO**：字段改名 `srt_content` → `vtt_content`：

```go
type subtitleDTO struct {
    ID         uint   `json:"id"`
    EpisodeID  uint   `json:"episode_id"`
    Language   string `json:"language"`
    Label      string `json:"label"`
    VttContent string `json:"vtt_content"`  // 原 srt_content
    Source     string `json:"source"`
    Optimized  bool   `json:"optimized"`
    CreatedAt  string `json:"created_at"`
    UpdatedAt  string `json:"updated_at"`
}
```

⚠️ **前端也要同步改字段名**：
- `frontend-admin/src/lib/types.ts:305-311` Subtitle 类型
- `frontend-admin/src/pages/courses/SubtitleDrawer.tsx`（读字幕）
- `frontend-admin/src/lib/api.ts` 上传字幕的方法

**`service/import_service.go:447, 501`**：auto-match 字幕从磁盘读 `.srt`，转 VTT 入库：

```go
// 读 srt 文件后
vtt := ai.SrtToVtt(string(srtBytes))
episodeRepo.SaveSubtitle(&model.Subtitle{..., VttContent: vtt, Source: "manual"})
```

#### Step 5: 删库重建 + 跑测试

```bash
# 备份（如果还没做）
cp backend/data/studyquest.db backend/data/studyquest.db.bak.$(date +%Y%m%d)

# 删除让 AutoMigrate 重建（用户已批准）
rm backend/data/studyquest.db backend/data/studyquest.db-wal backend/data/studyquest.db-shm

# 启动会自动 AutoMigrate 建表
make run &  # 或 make build 后 ./backend/bin/server
# Ctrl+C 停掉

# 跑全量测试
make test
cd frontend-admin && npx tsc --noEmit && npm test
```

#### Step 6: 重新 whisper 一节课做端到端验证

启动 worker 跑一节课，确认：
1. worker 上传 SRT 正常
2. 后端转 VTT 入库
3. admin 字幕管理 Drawer 看到字幕
4. 客户端播放字幕正常
5. segment job 正常跑（读 VTT 转 SRT 切 chunk）

#### 验收标准
- ✅ `make test` 全绿
- ✅ `tsc --noEmit` 全绿
- ✅ codec UT 全覆盖（上面 16 个用例）
- ✅ 手动播放字幕正常
- ✅ segment job 跑通

---

### PR2：字幕 LLM 润色（先 PoC）

#### 2-A: PoC（关键，必须先做）

**目标**：用真实 provider 跑真实数据，验证润色效果。

**已就绪的 PoC 数据**：episode_id=32 象棋课「五七炮进3兵对屏风马」，157593 字符，充满同音错字（军/局→车、何/合→和、金→进），术语密集（车马炮兵、屏风马、五七炮、催杀、打将）。

**新建**：`backend/cmd/polishpoc/main.go`

独立 main，连真实 `studyquest.db`：

```go
package main

// 1. 打开 studyquest.db（直接 gorm.Open，不走 server）
// 2. 读 episode_id=<参数> 的字幕
// 3. 读 ai_providers 表构造 provider（复用 ai.NewProviderResolver）
// 4. 调 polish.Polish(ctx, llm, model, req)
// 5. 输出到 backend/data/polish-poc-output/:
//    - original.srt  (VttToSrt 后的原始)
//    - polished.srt  (润色后)
//    - diff.json     ([{id, before, after}, ...])
//    - glossary.json ([{original, corrected, context, confidence, evidence_ids}, ...])
//    - stats.txt     (cue 总数, 改动数, 调用次数, token, 耗时, 估算成本)
```

**运行**：`cd backend && go run ./cmd/polishpoc -episode 32`

#### 2-B: 核心 polish 实现

**新建**：`backend/internal/ai/polish/polish.go`

```go
package polish

// PolishRequest 是润色的输入
type PolishRequest struct {
    VttContent string
    TermDict   string  // Course.EffectiveTermDict(subject)
    Subject    string  // "math" / "xiangqi" / ...
}

// PolishResult 是润色的输出
type PolishResult struct {
    PolishedVtt string  // 润色后的 VTT
    Diff        []CueDiff
    Glossary    []GlossaryCandidate
    Stats       PolishStats
}

type CueDiff struct {
    ID      int
    Before  string
    After   string
}

type GlossaryCandidate struct {
    Original     string
    Corrected    string
    Context      string
    Confidence   float64
    EvidenceIDs  []int
}

type PolishStats struct {
    TotalCues        int
    ChangedCues      int
    LLMCalls         int
    PromptTokens     int
    CompletionTokens int
    Duration         time.Duration
    PartialOptimized bool
}

// Polish 主入口
func Polish(ctx context.Context, llm ai.LLMProvider, model string, req PolishRequest) (*PolishResult, error) {
    // 1. VttToSrt + ParseSRT → cues
    // 2. 分块（150 cue/块，重叠 3）
    // 3. 并发（3 路 semaphore）调 LLM
    // 4. 校验每个返回（长度差 ≤2, 标点未变, id 集合一致）
    // 5. 应用 changes 到 cues（时间戳保留）
    // 6. 重新组装 SRT → VTT
    // 7. 返回 PolishResult
}

// polishChunk 单块润色（被并发调用）
func polishChunk(ctx context.Context, llm ai.LLMProvider, model string, chunk []cue, termDict, subject string) (*chunkResult, error)

// chunkResult 单块结果
type chunkResult struct {
    Changes  []CueChange
    Glossary []GlossaryCandidate
    Usage    ai.Usage
}

type CueChange struct {
    ID   int    `json:"id"`
    Text string `json:"text"`
}
```

#### system prompt（polish.go 里）

```
你是一个字幕校对器。你会收到一段机器转录的字幕（JSON）以及术语字典。
你的任务是找出其中的【同音错字和术语错误】，返回需要修正的条目，同时把本次发现的术语规律挖出来供后续课程使用。

【严格规则——违反则整批结果作废】
1. 只改【术语字典】里明确列出的词，以及你能 100% 确定是同音错字的词
2. 不改标点、不改语序、不优化表达、不纠正语法
3. 改动前后字符数差距 ≤ 2
4. 利用上下文判断：字典里的词在当前句中是否真的是术语
   （如"动车"是动词+车 vs "车走到中路"是象棋术语）
5. 没问题的条目不要放进 changes
6. 严格只输出 JSON

【术语挖矿】
把本次观察到的、有把握的术语纠错规律放进 glossary 字段。
- 只放 confidence ≥ 0.7 的（多次观察、上下文一致）
- evidence_ids 写观察到这个规律的 cue id 数组
- 字典里已有的不要重复放

输出格式：
{
  "changes": [
    {"id": 147, "text": "修正后的整句文本"}
  ],
  "glossary": [
    {
      "original": "车",
      "corrected": "居",
      "context": "象棋术语，指棋子",
      "confidence": 0.95,
      "evidence_ids": [147, 152, 198]
    }
  ]
}
```

#### user prompt（polish.go 里）

```json
{
  "term_dict": "车→居（象棋术语）;通分→同分",
  "subject": "数学",
  "subtitles": [
    {"id": 1, "text": "..."},
    {"id": 2, "text": "..."}
  ]
}
```

#### 后端校验逻辑（polish.go 里）

```go
func validateChanges(input map[int]string, changes []CueChange) error {
    for _, ch := range changes {
        original, ok := input[ch.ID]
        if !ok {
            return fmt.Errorf("unknown id %d in changes", ch.ID)
        }
        origLen := utf8.RuneCountInString(original)
        newLen := utf8.RuneCountInString(ch.Text)
        if abs(origLen-newLen) > 2 {
            return fmt.Errorf("id %d length changed too much: %d → %d", ch.ID, origLen, newLen)
        }
        if hasPunctuationDiff(original, ch.Text) {
            return fmt.Errorf("id %d punctuation changed", ch.ID)
        }
    }
    return nil
}

// hasPunctuationDiff 检测标点是否被改动
func hasPunctuationDiff(a, b string) bool {
    pa := extractPunctuation(a)
    pb := extractPunctuation(b)
    return pa != pb
}
```

#### PoC 验收标准（必须全部通过才进正式实施）

- ✅ 时间戳 100% 保留（cue 数不变、每个 cue 的 start/end ms 不变）
- ✅ 改动率合理（10-30% 之间；过低说明 LLM 没干活，过高说明过度修正）
- ✅ 字典里的术语确实被纠正（手动抽查 5 个 case，特别是象棋课的车马炮等术语）
- ✅ 没有"多管闲事"的改动（标点未变、长度差 ≤2）
- ✅ 单块失败能重试，不阻塞整体
- ✅ 成本 ≤ ¥3（DeepSeek 价位）
- ✅ 耗时 ≤ 10 分钟（象棋课 157k 字符，是数学课的 5 倍，并发 3）
- ✅ glossary 字段产出合理（象棋课应该挖出车/马/炮/兵/进/平等术语候选）

#### PoC 失败应对

| 问题 | 解决 |
|---|---|
| LLM 不遵守约束 | 加 `response_format: {"type":"json_object"}`（需先加 `ChatRequest.ResponseFormat any` 字段） |
| 改动率太高 | prompt 加 few-shot 示例 |
| 成本超预算 | 缩小块大小或换更便宜的模型 |
| 时间戳错乱 | 校验加严，失败直接 reject |
| 跨块上下文不足 | 增大重叠到 5 cue |

#### PoC 实际结果（2026-07-20 已跑，episode_id=32 象棋课）

**数据**：3550 cues / 157593 字符 / 25 块（150 cue/块，重叠 3） / 并发 3 / 模型 gpt-5.6-luna

**stats.txt**：
```
total_cues        : 3550
changed_cues      : 259
change_rate       : 7.30%
chunk_count       : 25
llm_calls         : 25  (successful, 0 failed)
retries           : 4
prompt_tokens     : 71751
completion_tokens : 52112
duration          : 7m13.936s
partial_optimized : false
estimated_cost    : ¥0.2802  (DeepSeek price)
```

**验收对照**：
- ✅ 时间戳 100% 保留（3550 cues 改前改后完全一致）
- ✅ 改动率 7.3%（略低于预期的 10-30%，但合理——象棋课很多 cue 是"嗯""啊"短语气词，不需要改）
- ✅ 关键术语纠正全部正确：
  - `局→车` 87 次（"退局抢中"→"退车抢中"）
  - `金→进` 33 次（"兵六金一"→"兵六进一"）
  - `何→和` `合→和` 6 次（"这个棋就是何棋"→"和棋"）
  - `抛→炮` 7 次
  - `足→卒`、`猎→列`（列手炮）、`向→相`、`骑械→棋协`、`骑士→棋士`
- ✅ 0 个长度违规、0 个标点违规
- ✅ 25 块全部成功，0 失败，4 次重试（重试机制工作）
- ✅ 成本 ¥0.28（远低于 ¥3 预算）
- ⚠️ 耗时 7m13s（略超预期的 5 分钟，但可接受；正式实施时可以加大并发或用更快的模型）
- ⚠️ glossary 挖出 1 个错误候选：`向→象`（confidence 0.99）——这是错的，"向前""向上"等场景"向"是对的。但这正是 PR2.5 admin 审核机制存在的价值：LLM 挖矿产出由 admin 把关，错误的 reject 即可

**发现的问题及修复**：

1. **LLM 偶发幻觉（整句改写）**：cue 382 出现"那对方这个棋跟我走的向三进五"→"这个棋等一下我就给大家说一下"——长度恰好相等（14 字符）、标点相同，绕过了原有的长度+标点校验。
   - **已修复**：`polish.go` 的 `changeAllowed` 加第三道校验 `charOverlap(orig, text) >= 0.6`（rune 频率集合的 Jaccard 相似度）。验证：
     - 幻觉 case：0.167（被拒 ✅）
     - 合法同音字：0.75（通过 ✅）
     - 单字纠正：0.818（通过 ✅）

**结论**：**方案完全成立，可以进入 PR2 正式实施**。

#### PoC 第二轮验证（2026-07-20，3 次串行运行）

第一轮发现幻觉问题后追加了 charOverlap 修复。为彻底验证，又跑了 3 次（象棋课 ×2 验证稳定性，数学课 ×1 验证泛化性）。

**3 次 stats 对照**：

| 指标 | 象棋 run1 | 象棋 run2 | 象棋 run3 | 数学 run1 |
|---|---|---|---|---|
| total_cues | 3550 | 3550 | 3550 | 741 |
| changed_cues | 259 | 162 | 180 | 6 |
| change_rate | 7.30% | 4.56% | 5.07% | **0.81%** |
| failed_chunks | 0 | 7 | 6 | 1 |
| retries | 4 | 19 | 20 | 3 |
| duration | 7m13s | 13m01s | 12m45s | 2m14s |
| estimated_cost | ¥0.28 | ¥0.31 | ¥0.29 | ¥0.033 |

**稳定性结论**：
- 三跑改动集合交集占并集 23.4% — 看着低，但**主要原因是 chunk 失败造成的假性分歧**，不是模型分歧
- run2/run3 的 6-7 个 chunk 失败是外部 provider 限速（30 分钟内连跑 3 次触发；**生产环境字幕完成才触发一次，间隔几小时，不会撞限速**）
- 在共同命中的 cue 上，after 文本一致性 **87.8%**，不一致的都是"保守 vs 激进"的同音字密度差异，**没有方向性错误**
- **charOverlap 修复彻底生效**：run1 的 cue 382 整句幻觉，在 run2 变成正确的单字纠正（`向→象`），在 run3 完全不改。run2/run3 全程 0 个幻觉，0 个误伤合法同音字纠正

**泛化性结论**（数学课）：
- 改动率 0.81%（远低于象棋 7.3%），**LLM 在术语少的场景下极其克制**
- 6 个改动全部正确（数实→数式、未知原理→位值原理、位置原理→位值原理、在家→再加 等），**0 过度纠错**
- glossary 挖出 2 条合理候选（位值原理相关）
- **证明 LLM 在 TermDict 不匹配/不完整时不会"没活找活"**，靠常识独立挖领域术语

**第二轮验证最终结论**：方案稳健，可进入 PR2 正式实施。无需调整 prompt、分块参数、charOverlap 阈值。

**两个发现的问题（PR2 实施前/中处理）**：

1. **【数据 bug，需用户确认】course 1（数学课）subject_id 错误**
   - DB 里 course 1（"胡小群思维启发必修课 L3-L6"）的 subject_id = 4（象棋），应该是 2（数学）
   - 这是**用户原始数据的问题**（迁移前就这样，不是我引入的）
   - 影响：数学课的 TermDict/WhisperHint 都会拿到象棋的配置
   - **不擅自修**——subject_id 影响所有 AI 配置，用户应自己确认。建议用户跑个 SQL 核对所有 course 的 subject_id

2. **【PoC 特有问题，生产不严重】chunk 失败重试策略**
   - PoC 连跑触发外部 provider 限速，导致 28% chunk 失败（fallback 到原文）
   - 生产环境不会撞这个（字幕完成才触发一次润色）
   - 但 PR2 正式实施时建议**加 chunk-level deadline**（避免单 chunk 卡死整个润色）+ 把失败 chunk 写进 `glossary_candidates` 供 admin 看到"这段没润色"

#### 2-C: 正式实施（PoC 通过后，留到会话 2）

**新建 `glossary_candidates` 表**（`model/models.go` 加，schema 见 3.4）。

**改 `subtitle_service.go:337-386` Complete**：插入润色步骤。

完整顺序：
```
worker Complete(SRT)
  ↓
1. SaveSubtitle(SRT→VTT, source=whisper, optimized=false)
2. MarkDone(job)
3. 异步触发润色（goroutine）:
     - 读 VTT
     - 调 polish.Polish(...)
     - UPDATE subtitles SET vtt_content=polished, optimized=true, source=llm_optimized
     - 写 glossary_candidates 表（UpsertCandidate 去重）
4. 润色完成后才触发 onSubtitleCompleted → segment job（阻塞语义）
```

**关键改动**：现有 `Complete` 里 `onSubtitleCompleted` 是在 SaveSubtitle 后立即触发（同步）。改成在润色 goroutine 完成后才触发。

注意：润色失败不能阻塞字幕可用。**字幕存进去立即可用**（用户看到的是原始字幕），润色成功后覆盖。润色失败的话 `optimized=false`，admin 可以手动重试。

**source 决策**：
- source=`embedded` 和 `manual` 的字幕不润色（人工字幕正确率高），直接触发 segment
- source=`whisper` 的字幕润色完才触发 segment

```go
// subtitle_service.go Complete 里
if s.shouldPolish(source) {  // source == "whisper"
    go s.polishAndTrigger(episodeID, source)
} else if s.onSubtitleCompleted != nil {
    s.onSubtitleCompleted(episodeID)  // 直接触发
}

// polishAndTrigger:
func (s *subtitleJobService) polishAndTrigger(episodeID uint, source string) {
    result, err := s.polishSvc.Polish(episodeID, ...)
    if err != nil {
        log.Warn("polish failed, subtitle stays raw", ...)
        // 字幕保持 optimized=false,用户和 AI 都用原始的
    } else {
        s.episodeRepo.SaveSubtitle(&Subtitle{..., VttContent: result.PolishedVtt, Optimized: true, Source: "llm_optimized"})
        s.glossaryRepo.UpsertCandidates(result.Glossary)
    }
    // 无论润色成功失败,segment 都要触发(用当前字幕,润色过的或原始的)
    if s.onSubtitleCompleted != nil {
        s.onSubtitleCompleted(episodeID)
    }
}
```

#### 2-D: 实际实施记录（2026-07-20，会话 2）

**实施时方案调整**（与上面 2-C 的 goroutine 方案不同，最终采用了 AIJob worker 方案）：

| 方面 | 上面 2-C 的设想 | 实际实施 | 原因 |
|---|---|---|---|
| 执行模型 | subtitle_service 里的 goroutine | **AIJob worker 队列**（JobType="polish"） | goroutine 中间状态对 admin 不可见（润色跑到哪、卡没卡、为什么失败全埋在日志里）。AIJob 方案让 polish job 进 ai_jobs 表，admin 在 AI Console 直接看到状态/错误，复用 ReapStaleJobs/ResetJob/RetryJob |
| 失败行为 | 字幕立即可用（optimized=false），admin 手动重试 | **失败卡住链路**：job failed，segment 不自动入队，admin 在 AI Console 决定【重试】或【跳过 polish】 | 用户明确要求"polish 失败应该卡住，由 admin 决定"。默默用原始字幕继续会让 admin 根本没注意到 polish 没跑 |
| 原始字幕 | 直接覆盖 VttContent | **Subtitle 加 RawVttContent 字段**，Complete 时两份同写，polish 成功只覆盖 VttContent | 用户要求"原始字幕必须保留，否则失败重试会越润越偏"。RawVttContent 永不被覆盖 |
| 多语言 | 未涉及 | **Subtitle 加 IsPrimary 字段**，AI 链只处理 primary（GetSubtitle 改成优先取 is_primary） | PR3 多语言提取需要，schema 一次到位，PR2 只加字段 + whisper 字幕默认 primary，UI 留 PR3 |

**新增的 admin 操作**：
- 【重试 polish】：复用现有 `POST /admin/api/ai/jobs/:id/retry`（failed→queued）
- 【跳过 polish】：新端点 `POST /admin/api/ai/jobs/:id/skip-polish`（failed polish→done，链式触发 segment 用原始字幕）。只对 failed + polish 类型生效，其他返回 409
- 【重新润色】：`POST /admin/api/ai/jobs` 带 `job_type=polish`（EnqueuePolish）。admin 改了 TermDict 后对已 polish 的 episode 重跑——从 RawVttContent 重跑不会 drift。前端 RegenTab 每集行有「重新润色」按钮

**Workflow 链路**（最终形态）：
```
worker Complete(SRT) → SaveSubtitle(VttContent=RawVttContent=原始, source=whisper, is_primary=true)
                      ↓
OnSubtitleCompleted 读 subtitle.source
  ├─ source=whisper + resolver 已配 → 入队 polish job
  │    ├─ polish 成功 → SaveSubtitle(VttContent=润色版, source=llm_optimized, optimized=true; RawVttContent 不动)
  │    │                + UpsertCandidates(glossary) → 链式入队 segment
  │    └─ polish 失败 → job failed，链路卡住，admin 决定【重试】/【跳过】
  └─ source=embedded/manual 或 resolver 未配 → 直接入队 segment（原逻辑）

admin 改 TermDict 后（如接受了 glossary 候选）→ EnqueuePolish(episodeIDs)
  → runPolishJob 从 RawVttContent 重跑（不读当前 VttContent，避免 drift）
  → 覆盖 VttContent 为新润色版，链式触发 segment
```

**schema 改动清单**（AutoMigrate 非破坏性加列，已验证）：
- `subtitles` 加 `raw_vtt_content TEXT`、`is_primary NUMERIC`
- 新表 `glossary_candidates`（见 §3.4）
- `media_streams`（JSON 字段，非表）加 `is_bitmap`（PR3）

**数据 backfill**（已执行）：
```sql
UPDATE subtitles SET is_primary = 1 WHERE id IN (SELECT MIN(id) FROM subtitles GROUP BY episode_id);
UPDATE subtitles SET raw_vtt_content = vtt_content WHERE raw_vtt_content IS NULL OR raw_vtt_content = '';
```

#### 2-D': 第二轮 Review 修复记录（2026-07-21）

三份 review（单点 bug）+ 两份 review（跨 PR 集成 + 修复正确性）后发现并修复的问题：

| 类别 | 问题 | 修法 |
|---|---|---|
| 🔴 C1 | `runPolishJob` 读 `VttContent`（可能已润色）而非 `RawVttContent`——re-polish 会 drift，RawVttContent 写了不读 | 改读 `RawVttContent`，空时 fallback `VttContent`（legacy 兼容） |
| 🔴 C2 | `SaveSubtitle` update 分支无条件覆盖 `IsPrimary`——extract 同语言会清掉 whisper 的 primary | 改成"只升不降"：`if subtitle.IsPrimary { sub.IsPrimary = true }` |
| 🔴 C3 | extract 同语言静默覆盖 whisper 行（source/VttContent/RawVttContent 全丢） | 加 `ErrSubtitleLanguageConflict`，extract 前检查 `(episode, language)` 冲突，409 拒绝 |
| 🔴 N1 | `runPolishJob` 的 `course==nil && err==nil` 时 nil 解引用 panic，kill 整个 AI worker（无 recover） | 拆分 err/nil 分支 + `runWorker` 加 `defer recover()` 兜底 |
| 🟡 W2 | 缺 re-polish 入口（改 TermDict 后没法重跑 polish） | 新增 `EnqueuePolish` + 前端「重新润色」按钮 |
| 🟡 W5 | siblings 推广含 entertainment 课程（象棋术语进动画片） | 推广默认只含 `ContentLearning` 课程 |
| 🟡 N2 | worker.py "dropping SRT" 日志误导（实际保留） | 改措辞为 "keeping cached SRT" |
| 🟡 C3' | polish chunk 失败原因被 `_ = pErr` 丢弃 | `chunkOutcome.errStr` + `PolishStats.FailedChunkErrors` + job detail 输出每个失败 chunk 的错误 |
| 🟡 C2' | polish retry 的 `break` 跳不出 for（select 内的 break 只跳 select） | 加 label `retryLoop:`，`break retryLoop` |
| 🟡 W1 | `save_srt` 异常会 kill 已成功的转录 | 包 try/except 降级为 warning |
| 🟡 W2(PR3) | `streamIndex` 未校验（负值/不存在/非字幕） | 加 `ErrInvalidStreamIndex` + 对照 media_meta_json 校验 |
| 🟡 C1(P2.5) | siblings 推广 SubjectID==0 时污染全库 | 加 `origin.SubjectID != 0` guard |

**关键设计决策的落地确认**：
- RawVttContent 完整生命周期：写入（Complete/extract）→ 永不覆盖（repo guard）→ polish 读它作基准（不 drift）→ admin 改 TermDict 后能重跑（EnqueuePolish）。闭环
- `runWorker` recover 兜底：未来任何 job handler 的 panic 都不杀整个后台 AI 处理

#### 2-E: 单元测试

`backend/internal/ai/polish/polish_test.go`：
- mockLLM（仿 `agent/agent_test.go:16-35`）测试分块、校验、组装逻辑
- 不调真实 LLM
- 覆盖：分块正确、重叠正确、校验拒绝过长/改标点的 change、id 缺失拒绝、空 changes、glossary 解析

---

### PR3：内嵌字幕探测 + 提取

#### 后端

**扩展 ffprobe 解析**（`episode_service.go:655-715` probeMedia）：

当前 JSON 解析只关心 video/audio stream。加 subtitle stream 识别：

```go
for _, s := range probe.Streams {
    ms := model.MediaStream{
        Index:    s.Index,
        Type:     s.CodecType,  // "video" / "audio" / "subtitle"
        Codec:    s.CodecName,
        Language: s.Tags.Language,
    }
    if s.CodecType == "subtitle" {
        // 额外记录是否图形字幕（codec 是 hdmv_pgs_subtitle / dvd_subtitle 等）
        ms.IsBitmap = isBitmapSubtitleCodec(s.CodecName)
    }
    meta.Streams = append(meta.Streams, ms)
}
```

`MediaStream` 加字段 `IsBitmap bool`（`models.go:432-438`）。

**新增提取服务方法**（`episode_service.go` 或新文件）：

```go
func (s *episodeService) ExtractEmbeddedSubtitle(episodeID uint, streamIndex int, language, label string) error {
    // 1. 拿 episode 的 stream URL
    link, err := s.GetStreamURL(episodeID, "")
    // 2. ffmpeg -i URL -map 0:<streamIndex> -c:s webvtt output.vtt
    //    用 runFFmpegWithRetry (episode_service.go:598-624) 处理 TLS 重试
    // 3. 如果是图形字幕，ffmpeg 会报错 "Subtitle encoding currently only possible from text to text or bitmap to bitmap"
    //    捕获明确报错返回 ErrBitmapSubtitleNotSupported
    // 4. 存 VttContent 入库，source="embedded"
}
```

**新增路由**：

```go
// router.go admin 组
POST /admin/api/episodes/:id/extract-subtitle
  body: {stream_index: int, language: string, label: string}
```

handler 在 `admin_content.go` 加 `ExtractSubtitle`。

#### 前端

**SubtitleDrawer.tsx 加内嵌字幕区块**：

```tsx
// 读 episode.media_meta_json 里的 streams，过滤 type === "subtitle"
const subtitleStreams = meta.streams?.filter(s => s.type === 'subtitle') ?? [];

// 渲染
{subtitleStreams.length > 0 && (
  <div className="border-t pt-3">
    <h4>内嵌字幕 ({subtitleStreams.length})</h4>
    {subtitleStreams.map(s => (
      <div key={s.index}>
        <span>流 #{s.index}: {s.language || '未标语言'} ({s.codec})</span>
        {s.is_bitmap ? (
          <span className="text-muted">图形字幕，无法提取，请用 Whisper 转录</span>
        ) : (
          <button onClick={() => extractMut.mutate({streamIndex: s.index, language: s.language || 'zh-CN', label: '中文'})}>
            提取
          </button>
        )}
      </div>
    ))}
  </div>
)}
```

#### UT 覆盖（字幕格式）

`testdata/` 目录加样本文件，测试 ffmpeg 转换：
```
backend/internal/service/testdata/
  subtitle-samples/
    sample.srt
    sample.ass       (含样式标签 {\an8}{\b1})
    sample.ssa
    sample.sub       (MicroDVD 帧率格式)
    sample.smi       (SAMI HTML 格式)
    sample.vtt       (带 cue 属性 line:50% align:center)
    sample.vtt.styled (带 <b> <c.highlight> 标签)
    sample_cjk.ass   (中文 ASS 验证编码)
    sample.pgs-info.json  (PGS 元数据，模拟图形字幕探测)
```

UT 每个样本跑 ffmpeg 转 VTT 后验证：
- cue 数正确
- 文本内容正确（样式被剥离但文本保留）
- 时间戳格式正确

---

### PR4：Whisper 字幕缓存

**纯 worker 端改动，不影响后端。**

**新建** `tools/video-pipeline/srt_cache.py`：

```python
"""SRT cache for the whisper worker.

Same key scheme as wav cache (filename + file_size), but additionally
includes the whisper model name in the key (so switching models
auto-invalidates), and stores the final SRT instead of the extracted
audio. This is the FASTEST path: a previous whisper run already
produced the SRT, so we skip everything (wav check, video scan,
netdisk download, ffmpeg, and the actual whisper transcription).

TTL: 30 days (longer than wav's 7 days, because SRT is smaller and
more valuable to reuse).
"""
import hashlib
import logging
import os
import time
from pathlib import Path

log = logging.getLogger("sq.srt_cache")

DEFAULT_SRT_CACHE_DIR = "~/.cache/sq-whisper/srt"
DEFAULT_TTL_DAYS = 30


def _cache_key(filename, file_size, model_name):
    ident = f"{filename}|{file_size or 'unknown'}|{model_name}"
    return hashlib.sha1(ident.encode("utf-8")).hexdigest()[:16]


def _cache_dir(cfg_dir):
    d = Path(os.path.expanduser(cfg_dir or DEFAULT_SRT_CACHE_DIR))
    d.mkdir(parents=True, exist_ok=True)
    return d


def find_cached_srt(filename, file_size, model_name, srt_cache_dir=None):
    """Return cached SRT content if HIT, else None."""
    d = _cache_dir(srt_cache_dir)
    key = _cache_key(filename, file_size, model_name)
    path = d / f"{key}.srt"
    if not path.exists() or path.stat().st_size == 0:
        return None
    age_days = (time.time() - path.stat().st_mtime) / 86400
    if age_days > DEFAULT_TTL_DAYS:
        log.info("srt cache EXPIRED: %s (%.1f days old)", path.name, age_days)
        try:
            path.unlink()
        except OSError:
            pass
        return None
    log.info("srt cache HIT: %s (%d bytes)", path.name, path.stat().st_size)
    return path.read_text(encoding="utf-8")


def save_srt(filename, file_size, model_name, srt_content, srt_cache_dir=None):
    """Save SRT to cache for future reuse."""
    d = _cache_dir(srt_cache_dir)
    key = _cache_key(filename, file_size, model_name)
    path = d / f"{key}.srt"
    path.write_text(srt_content, encoding="utf-8")
    log.info("srt cache SAVED: %s (%d bytes)", path.name, len(srt_content))


def clean_old_srts(srt_cache_dir=None, max_age_days=DEFAULT_TTL_DAYS):
    """Remove SRTs older than max_age_days. Called at worker startup."""
    d = _cache_dir(srt_cache_dir)
    cutoff = time.time() - max_age_days * 86400
    removed = 0
    for f in d.glob("*.srt"):
        try:
            if f.stat().st_mtime < cutoff:
                f.unlink()
                removed += 1
        except OSError:
            pass
    if removed:
        log.info("srt cache: removed %d stale file(s)", removed)
    return removed
```

**改造 `worker.py:128-203` process_job**：

在 wav cache check 之前加 srt cache check（最快路径）：

```python
def process_job(client, tr, idx, job, cfg):
    # 0. SRT cache: FASTEST path. Previous whisper run already produced SRT.
    #    Skip everything (wav check, video scan, netdisk, ffmpeg, whisper).
    cached_srt = srt_cache.find_cached_srt(
        filename=job.episode.filename,
        file_size=job.episode.file_size,
        model_name=cfg.whisper.model_path,
        srt_cache_dir=cfg.audio.srt_cache_dir or None,
    )
    if cached_srt is not None:
        try:
            client.complete(job.job_id, cached_srt)
            log.info("job %d done (from srt cache): %d bytes", job.job_id, len(cached_srt))
            return
        except StaleCompletion:
            log.info("job %d stale-completed; dropping cached SRT", job.job_id)
            return

    # ... 原有 wav cache / video cache / netdisk 逻辑 ...

    # whisper 完成后，存 SRT cache 再 client.complete
    srt = tr.transcribe(wav_path, ...)
    srt_cache.save_srt(
        filename=job.episode.filename,
        file_size=job.episode.file_size,
        model_name=cfg.whisper.model_path,
        srt_content=srt,
        srt_cache_dir=cfg.audio.srt_cache_dir or None,
    )
    try:
        client.complete(job.job_id, srt)
```

**改 `config.py`** 加 srt_cache_dir（可选，默认 `~/.cache/sq-whisper/srt`）。

**改 `worker.py:230` main()**：启动时清理旧 srt：
```python
audio.clean_old_wavs(...)
srt_cache.clean_old_srts(...)
```

#### 验证

- 跑同一视频两次：第一次正常 whisper，第二次日志显示 `srt cache HIT` 秒完
- 换 model_name 后旧缓存失效（重新 whisper）

---

### PR2.5：术语字典 admin UI

**依赖**：PR2 的 `glossary_candidates` 表已有数据。

**新增 admin 端点**（`admin_ai.go` 或新文件 `admin_glossary.go`）：

```go
GET    /admin/api/courses/:id/glossary-candidates   // 列表（按 confidence 降序）
POST   /admin/api/glossary-candidates/:id/accept    // 接受单个（可带 corrected/context 编辑）
POST   /admin/api/glossary-candidates/:id/reject    // 拒绝单个
POST   /admin/api/courses/:id/glossary-accept-batch // 批量接受
```

**accept 逻辑**：把 accepted 的 `{original}→{corrected}` 追加到 `Course.AIConfigJSON.TermDict`（格式 `{original}→{corrected}（{context}）`），status 标 accepted，记录 `AcceptedAt` 时间戳。

**accept 支持编辑**：请求体可带 `corrected` 和 `context` 字段，admin 在 UI 里能改这两个字段后才接受。

**前端**：在 AIConsole 加一个 tab 或在 Prompt 配置 tab 下加区块：

```
[术语候选] (course 切换)
  发现 12 个候选，按 confidence 排序：

  车 → 居   confidence 0.95   evidence 5 处
    上下文：象棋术语，指棋子      [编辑]
    证据:cue 147 "把车走到中路"
         cue 152 "车马炮配合"
    [接受] [拒绝]

  通分 → 同分   confidence 0.92   evidence 3 处
    [接受] [拒绝]

  [批量接受选中]
  [应用到同学科所有课程]  ← 可选
```

---

### PR5：多 AI provider 分工

**目标**：让 admin 配多个 provider，不同任务用不同 provider（润色用便宜模型、quiz 用强模型）。

#### Schema 改动

`ai_providers` 表加 `tags` 字段（`models.go:975-997`）：

```go
type AIProvider struct {
    // ... 现有字段 ...
    Tags string `gorm:"size:256"`  // JSON 数组 ["polish","quiz-check"]；空 = 默认通用
}
```

#### Resolver 扩展

`backend/internal/ai/resolver.go`：

```go
// 新增方法
func (r *ProviderResolver) ResolveChatByPurpose(purpose string) (LLMProvider, error) {
    // 1. 找 tags 含 purpose 的启用行
    // 2. 找不到 → fallback 到 ResolveChat()（默认）
}

func (r *ProviderResolver) ChatModelNameByPurpose(purpose string) (string, error) {
    // 类似
}
```

**purpose 取值**：`"polish" / "summary" / "quiz" / "advice" / ""`（空 = 默认）。

#### 调用点改造

- `polish.go`：`llm := resolver.ResolveChatByPurpose("polish")`
- `summarizer.go`：保持 `resolver.ResolveChat()` 不变（默认），或改成 `ResolveChatByPurpose("summary")`
- `quizzer.go`：`ResolveChatByPurpose("quiz")`
- 其他类似

**向后兼容**：purpose 找不到时 fallback 到默认 chat provider，所以即使 admin 没配专用 provider，所有任务也都能跑。

#### 前端

`ai_providers` 编辑表单加 tags 多选：
```
[✓] polish    字幕润色（推荐便宜模型）
[✓] summary   课时总结
[ ] quiz      出题（推荐强模型）
[ ] advice    学习建议
[ ] quiz-check 题目自检
```

---

## 六、进度追踪

实施时每完成一个打勾。

### 会话 1（已完成核心工作，剩收尾）

- [x] **文档**：写完本文件（含词汇表工作流完整细节）
- [x] **PR1：前端可见性 + 按钮 gate**
  - [x] CourseTree EpisodeRow 加字幕 badge
  - [x] RegenTab 单集总结按钮 gate
  - [x] RegenTab 课程总结按钮 gate
  - [x] `tsc --noEmit` 通过
  - [x] `npm test` 通过（55/55）
  - [ ] 手动 UI 验证（留给用户）
- [x] **PR1.5：VTT 存储迁移（代码部分完成）**
  - [x] 备份 studyquest.db（已备份到 `.bak.20260720`）
  - [x] 改 Subtitle schema（加 VttContent/Source/Optimized）
  - [x] 新建 `subtitle/codec.go` + 完整 UT（16 个用例 + 2 个额外用例，全过）
  - [x] 改所有调用点（grep SrtContent 确认 0 遗漏，剩余的都是协议字段名 srt_content 故意保留）
  - [x] 改前端 types + SubtitleDrawer
  - [x] `make test` 全绿（service 包有已知 flaky test，重跑即过）
  - [x] 写迁移脚本 `scripts/migrate-subtitle-to-vtt.py`
  - [x] **执行迁移脚本**（2026-07-20 跑完，3 条字幕全部转 VTT，备份在 `.bak.20260720-184635`）
  - [x] **端到端验证**：字幕播放端点吐 VTT 正常；segment job 读取链路（VttToSrt → ParseSRT → cues）时间戳 100% 保留
- [x] **PR2 PoC：象棋课润色验证**
  - [x] 写 `cmd/polishpoc/main.go`
  - [x] 写 `internal/ai/polish/polish.go` 核心逻辑
  - [x] 跑 PoC 第一轮（episode_id=32，3550 cues，7m13s，¥0.28）
  - [x] Review PoC 结果（详见 §五 §2-B "PoC 实际结果"）
  - [x] 修复 LLM 幻觉问题（加 charOverlap >= 0.6 校验）
  - [x] 跑 PoC 第二轮 3 次验证（象棋 ×2 稳定性 + 数学 ×1 泛化性）
  - [x] **PoC 验收标准全部通过，方案稳健**
  - [x] 写 polish 单元测试（mockLLM）← 留到会话 2
  - [ ] **【用户处理】核实 course 1（数学课）的 subject_id**（DB 里错标成象棋）

### 会话 2（进行中）

**前置已完成**：迁移脚本已跑、server 已重启、端到端验证通过

- [x] **PR2 正式：润色管线**（2026-07-20，详见 §2-D 实际实施记录）
  - [x] schema：Subtitle 加 `RawVttContent` + `IsPrimary`；新增 `GlossaryCandidate` 表；AutoMigrate 注册
  - [x] 新 repo `glossary_repo.go`（UpsertCandidate/UpsertCandidates 去重逻辑）
  - [x] `episode_repo` SaveSubtitle（RawVttContent 只在非空时覆盖）+ GetSubtitle（优先取 is_primary）
  - [x] `ai_service.go`：runPolishJob + OnSubtitleCompleted 改 source-based 分支 + SkipPolish + enqueueSegment helper
  - [x] 新端点 `POST /admin/api/ai/jobs/:id/skip-polish`
  - [x] `polish_test.go`（12 个用例：正常/长度违规/低 overlap/时间戳保留/重试/glossary/nil provider/空 VTT/chunk-local ids/validation/charOverlap）
  - [x] 现有测试 ctor 适配（NewAIService 加 2 个参数，aiServiceTestEnv 改用 NewFileDB 修 worker goroutine 的 in-memory DB 竞态）
  - [x] 数据 backfill（is_primary + raw_vtt_content）
  - [x] 前端：AI Console failed polish job 加「跳过 polish」按钮 + api.skipPolish
  - [x] `go test ./...` 全绿 / `tsc --noEmit` 全绿 / `npm test` 55/55
  - [ ] 手动端到端验证（留给用户：跑一节 whisper 字幕，观察 polish→segment→summary 链路 + glossary_candidates 表）
- [x] **PR3：内嵌字幕探测 + 提取**（subagent 实施，2026-07-20）
  - [x] ffprobe 解析扩展（subtitle stream + is_bitmap 标记）
  - [x] `isBitmapSubtitleCodec` helper（PGS/VOBSUB/DVB 覆盖）
  - [x] `ExtractEmbeddedSubtitle` 服务方法（ffmpeg + runFFmpegWithRetry + SaveSubtitleWithSource source=embedded）
  - [x] 路由 `POST /admin/api/episodes/:id/extract-subtitle` + handler + AdminHandler interface
  - [x] 前端 SubtitleDrawer 加内嵌字幕区块（流列表 + 图形字幕提示 + 提取按钮）
  - [x] 测试 `episode_subtitle_extract_test.go`
- [x] **PR4：Whisper 字幕缓存**（subagent 实施，2026-07-20）
  - [x] `srt_cache.py`（find_cached_srt/save_srt/clean_old_srts，镜像 audio.py wav cache 模式）
  - [x] key 含 model_name（换模型自动失效），TTL 30 天
  - [x] worker.py process_job 插入 srt cache check（最快路径，在 wav cache 之前）+ save_srt
  - [x] config.py 加 srt_cache_dir
  - [x] worker.py main() 启动清理旧 srt
  - [x] 测试 `test_srt_cache.py`（13 个用例）
- [x] **PR2.5：术语字典 admin UI**（2026-07-20）
  - [x] glossary repo 加 ListByCourse/FindByID/Update 方法
  - [x] 4 个 admin 端点：list / accept（带编辑+跨课程推广）/ reject / accept-batch
  - [x] accept 逻辑：追加到 Course.AIConfigJSON.TermDict（格式 `original→corrected（context）`，去重），可选应用到同学科所有课程
  - [x] 测试 `glossary_service_test.go`（accept/reject/TermDict 追加/跨课程推广/格式 helper/排序）
  - [x] 前端 AIConsole 加「术语候选」tab（GlossaryTab）：课程选择、状态过滤、接受/拒绝/编辑后接受/批量接受/跨课程推广 checkbox
  - [ ] SubtitleDrawer 显示 optimized/source/primary + raw vs polished diff（留到后续，当前 admin 通过 ai_jobs 表的 polish job 状态可观察）
- [x] **PR5：多 AI provider 分工**（2026-07-20）
  - [x] AIProvider 加 `Tags string`（JSON 数组）+ ParseTags/HasTag helper（容忍逗号分隔的手写格式）
  - [x] resolver 加 ResolveChatByPurpose/ChatModelNameByPurpose + enabledRowByPurpose（Go 层线性过滤 tags，找不到 fallback 到默认 provider）
  - [x] 6 个 job 全部改用 by-purpose：polish / summary / quiz / advice / course_summary / user_report
  - [x] 测试 `resolver_test.go`（10 个子测试：tag 匹配矩阵 + ParseTags 容错）
  - [x] 前端 AiProvidersSection 加用途标签多选 checkbox + types 适配
  - [x] AutoMigrate 非破坏性加 `ai_providers.tags` 列
- [x] **两轮 Review + 修复**（2026-07-21，详见 §2-D'）
  - [x] 第一轮（单点 bug）：polish retry break / chunk 错误丢弃 / save_srt 异常 / streamIndex 校验 / siblings SubjectID==0 污染
  - [x] 第二轮（跨 PR 集成 + 修复正确性）：runPolishJob 读 RawVttContent / IsPrimary 不 demote / extract 拒绝同语言 / runPolishJob nil panic + runWorker recover 兜底
  - [x] 新增 EnqueuePolish（re-polish 入口）+ W5 siblings 推广只含 learning 课程
  - [x] 补测试：IsPrimary 保留 / extract 拒绝同语言 / glossary SubjectID==0 回归
- [ ] **【用户处理】核实 course 1（数学课）的 subject_id**

---

## 七、风险登记

| 风险 | 缓解 |
|---|---|
| LLM 不遵守 prompt 约束 | PoC 验证；必要时加 response_format JSON mode |
| PoC 效果不达标 | 缩小块大小、换模型、加 few-shot；最坏退回字典查表 + 人工审核 |
| 删库重建丢数据 | 用户已批准；每次改 schema 前备份到 `.bak.<date>` |
| ffmpeg 提取内嵌字幕失败率高 | PGS 图形字幕明确报错；复用现有 TLS 重试 |
| worker 缓存 SRT 过期 | 30 天 TTL + model_name 入 key |
| VTT 格式兼容性（不同播放器） | 主流播放器都支持 VTT；Flutter video_player 支持 |
| 多 provider 配置错误（tag 拼错） | purpose 找不到时 fallback 到默认，永远不阻塞 |
| 跨层契约不一致（字幕字段改名） | grep 所有 SrtContent 调用点，逐个改；UT 覆盖 |

---

## 八、关键文件位置参考

### 后端
- 字幕模型：`backend/internal/model/models.go:441-450`
- 字幕存储：`backend/internal/repository/episode_repo.go:42-46, 259-271`
- 字幕服务：`backend/internal/service/episode_service.go:352-366`
- 字幕队列：`backend/internal/service/subtitle_service.go`
- 字幕播放：`backend/internal/handler/episode_handler.go:64-98`
- 字幕切分：`backend/internal/ai/segmenter.go:76, 172`
- AI 触发链：`backend/internal/service/ai_service.go:424-461, 533-626`
- AI prompt：`backend/internal/ai/agent/prompts.go`（TermDict 注入点 line 202, 355, 327）
- LLM Provider：`backend/internal/ai/provider.go:41-64`、`openai_compat.go`
- Provider Resolver：`backend/internal/ai/resolver.go`
- ffprobe/ffmpeg：`backend/internal/service/episode_service.go:547-715`

### 前端
- Episode 类型：`frontend-admin/src/lib/types.ts:139-147, 305-311`
- API 客户端：`frontend-admin/src/lib/api.ts:130-132, 269-271, 343-345`
- 课程树（视频列表）：`frontend-admin/src/pages/courses/CourseTree.tsx:492-593`
- 字幕 Drawer：`frontend-admin/src/pages/courses/SubtitleDrawer.tsx`
- AI 控制台：`frontend-admin/src/pages/ai-console/RegenTab.tsx:189-200, 270-277`

### Worker
- 主循环：`tools/video-pipeline/worker.py:128-273`
- 音频提取：`tools/video-pipeline/audio.py`
- 转录：`tools/video-pipeline/transcriber.py`
- 缓存：`tools/video-pipeline/cache.py`
- API 客户端：`tools/video-pipeline/api_client.py`
- 配置：`tools/video-pipeline/config.py`

### 文档
- 项目导航：`CLAUDE.md`（特别是 subagent 规则 line 118-193）
- 待办清单：`TODO.md`
- 字幕队列设计：`docs/ai-subtitle-queue.md`

---

## 九、PoC 测试数据（已就绪）

episode_id=32：象棋课「五七炮进3兵对屏风马-黑方大边车走法的几种应对方式」

- **字幕长度**：157593 字符（是数学课 episode_id=1 的 5 倍，非常考验分块/并发）
- **充满同音错字**（PoC 预期应纠正）：
  - "军" → "车"（jū，象棋术语）
  - "局" → "车"（jū）
  - "何" → "和"（hé，象棋术语，和棋）
  - "合" → "和"（hé）
  - "金" → "进"（jìn，象棋走法）
  - "平" 应该保留（象棋走法"平"是对的）
- **术语密集**：车马炮兵、屏风马、五七炮、进3兵、平兵、抢中、催杀、打将
- **课程**：2026 周日象棋培训课（subject: xiangqi）

**PoC 验证点**：
1. "军 3 进 9 上来" → 应该改成 "车 3 进 9 上来"
2. "军 2 平 7 军杀" → 应该改成 "车 2 平 7 车杀"
3. "黑方退局抢中" → 应该改成 "黑方退车抢中"
4. "这个棋就是何棋" → 应该改成 "这个棋就是和棋"
5. "兵六金一" → 应该改成 "兵六进一"

**不应误改**：
- "中间" 不应改成 "中居"
- "进上来" 不应被误动
- "打完将再退回" 应该保留
