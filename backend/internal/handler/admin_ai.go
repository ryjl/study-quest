package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/service"
)

// aiProviderDTO is the JSON shape for an AIProvider row. The api_key field is
// handled specially on READ (never echoed back — see toAIProviderDTO) and on
// WRITE (empty on update = "don't change", see mergeAIProvider). This matches
// the Settings.tsx admin-password convention ("leave blank = don't modify").
type aiProviderDTO struct {
	ID           uint   `json:"id"`
	Capability   string `json:"capability"`    // chat | embedding | rerank
	Name         string `json:"name"`          // display name
	ProviderType string `json:"provider_type"` // openai_compat | onnx_local
	BaseURL      string `json:"base_url"`      // chat relay base; empty for onnx_local
	APIKey       string `json:"api_key"`       // write-only: never echoed back on read
	ModelName    string `json:"model_name"`    // model id (chat) or model dir (onnx)
	ExtraJSON    string `json:"extra_json,omitempty"`
	// Tags is the provider's purpose tags as a JSON array (e.g. ["polish"]).
	// On WRITE the admin UI sends an array; we re-marshal it to a canonical JSON
	// string before persisting. On READ we parse the stored string back to an
	// array so the UI round-trips cleanly. nil/empty = general-purpose (the
	// default fallback for any task with no purpose-tagged provider).
	Tags      []string `json:"tags,omitempty"`
	IsEnabled bool     `json:"is_enabled"`
}

// toAIProviderDTO converts a model row to its DTO, STRIPPING the api_key. The
// key is a secret; the admin UI shows a masked "leave blank to keep" field
// rather than the real value, so the list/detail endpoints must never return
// it. (Same posture the admin-password path takes; plaintext-at-rest encryption
// is tracked as a separate cross-cutting task.)
func toAIProviderDTO(p model.AIProvider) aiProviderDTO {
	return aiProviderDTO{
		ID:           p.ID,
		Capability:   p.Capability,
		Name:         p.Name,
		ProviderType: p.ProviderType,
		BaseURL:      p.BaseURL,
		APIKey:       "", // never echo back
		ModelName:    p.ModelName,
		ExtraJSON:    p.ExtraJSON,
		Tags:         p.ParseTags(),
		IsEnabled:    p.IsEnabled,
	}
}

// tagsToStorage normalizes the DTO's Tags slice into the canonical JSON string
// stored on the model. nil/empty → "" (general-purpose). We always re-marshal
// (rather than storing the raw input) so equivalent inputs land on the same
// bytes and the resolver's parse path is exercised identically by write+read.
func tagsToStorage(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	out, err := json.Marshal(tags)
	if err != nil {
		return ""
	}
	return string(out)
}

// ListAIProviders returns all configured AI providers.
// GET /admin/api/ai/providers
func (h *adminHandler) ListAIProviders(c *gin.Context) {
	if h.aiProviderRepo == nil {
		c.JSON(http.StatusOK, []aiProviderDTO{})
		return
	}
	providers, err := h.aiProviderRepo.List()
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]aiProviderDTO, 0, len(providers))
	for _, p := range providers {
		out = append(out, toAIProviderDTO(p))
	}
	c.JSON(http.StatusOK, out)
}

// CreateAIProvider creates a new AI provider config.
// POST /admin/api/ai/providers
func (h *adminHandler) CreateAIProvider(c *gin.Context) {
	if h.aiProviderRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI subsystem not configured"})
		return
	}
	var req aiProviderDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	if msg := validateAIProvider(req, true); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	p := model.AIProvider{
		Capability:   req.Capability,
		Name:         req.Name,
		ProviderType: req.ProviderType,
		BaseURL:      req.BaseURL,
		APIKey:       req.APIKey,
		ModelName:    req.ModelName,
		ExtraJSON:    req.ExtraJSON,
		Tags:         tagsToStorage(req.Tags),
		IsEnabled:    req.IsEnabled,
	}
	if err := h.aiProviderRepo.Create(&p); err != nil {
		respondError(c, err)
		return
	}
	h.invalidateAI(req.Capability)
	c.JSON(http.StatusOK, toAIProviderDTO(p))
}

// UpdateAIProvider updates an existing provider. A blank api_key in the request
// means "keep the existing secret" — this lets the admin edit other fields
// without re-entering the key every time (which they can't see).
// PUT /admin/api/ai/providers/:id
func (h *adminHandler) UpdateAIProvider(c *gin.Context) {
	if h.aiProviderRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI subsystem not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	existing, err := h.aiProviderRepo.FindByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider 不存在"})
		return
	}
	var req aiProviderDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	if msg := validateAIProvider(req, false); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}

	// Merge: overwrite mutable fields; preserve the existing key when the
	// request left api_key blank (the "don't modify" convention).
	existing.Capability = req.Capability
	existing.Name = req.Name
	existing.ProviderType = req.ProviderType
	existing.BaseURL = req.BaseURL
	existing.ModelName = req.ModelName
	existing.ExtraJSON = req.ExtraJSON
	existing.Tags = tagsToStorage(req.Tags)
	existing.IsEnabled = req.IsEnabled
	if req.APIKey != "" {
		existing.APIKey = req.APIKey
	}

	if err := h.aiProviderRepo.Update(existing); err != nil {
		respondError(c, err)
		return
	}
	// Invalidate both old + new capability, since capability itself can change.
	h.invalidateAI(existing.Capability)
	h.invalidateAI(req.Capability)
	c.JSON(http.StatusOK, toAIProviderDTO(*existing))
}

// DeleteAIProvider removes a provider config.
// DELETE /admin/api/ai/providers/:id
func (h *adminHandler) DeleteAIProvider(c *gin.Context) {
	if h.aiProviderRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI subsystem not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	// Look up capability before delete so we can invalidate the right cache slot.
	existing, _ := h.aiProviderRepo.FindByID(id)
	if err := h.aiProviderRepo.Delete(id); err != nil {
		respondError(c, err)
		return
	}
	if existing != nil {
		h.invalidateAI(existing.Capability)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// TestAIProvider tests connectivity for one provider by actually building it
// and calling Ping. For chat this sends a tiny completion to the relay; for
// embedding it loads the ONNX model and embeds one word. This is the most
// expensive admin action but the only way to truly verify the full path works.
// POST /admin/api/ai/providers/:id/test
func (h *adminHandler) TestAIProvider(c *gin.Context) {
	if h.aiProviderRepo == nil || h.aiResolver == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI subsystem not configured"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	p, err := h.aiProviderRepo.FindByID(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider 不存在"})
		return
	}

	// Ping with a generous-but-bounded timeout: model load (ONNX) or a slow
	// relay can take a few seconds, but we don't want a stuck endpoint to hang
	// the admin UI forever.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// Rebuild fresh for the test (don't trust the cache — the admin may have
	// just edited the row and the cache may still hold the old instance).
	h.aiResolver.Invalidate(p.Capability)

	start := time.Now()
	var pingErr error
	switch p.Capability {
	case model.AICapabilityChat:
		var llm ai.LLMProvider
		llm, pingErr = h.aiResolver.ResolveChat()
		if pingErr == nil {
			pingErr = llm.Ping(ctx)
		}
	case model.AICapabilityEmbedding:
		var emb ai.Embedder
		emb, pingErr = h.aiResolver.ResolveEmbedder()
		if pingErr == nil {
			pingErr = emb.Ping(ctx)
		}
	case model.AICapabilityRerank:
		// Not wired in MVP; surface clearly rather than pretending.
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": "rerank capability not implemented yet"})
		return
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "未知 capability: " + p.Capability})
		return
	}
	latency := time.Since(start).Milliseconds()

	if pingErr != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": pingErr.Error(), "latency_ms": latency})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "连接成功", "latency_ms": latency})
}

// ListAIProviderModels fetches the model catalog from an OpenAI-compatible
// relay using the provided base_url + api_key (NOT a saved row). This lets the
// admin UI populate a model dropdown BEFORE saving a config — the "填完 base_url
// + key 即拉取" flow. The probe is anonymous-by-design: it only reads /v1/models,
// never sends chat content.
// POST /admin/api/ai/providers/models  body: {base_url, api_key}
func (h *adminHandler) ListAIProviderModels(c *gin.Context) {
	var req struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	if req.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "base_url 为必填"})
		return
	}
	// Bounded timeout: /v1/models is fast, but a misconfigured/relay behind a
	// slow proxy shouldn't hang the admin UI.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	prov := ai.NewOpenAICompatProvider(req.BaseURL, req.APIKey)
	models, err := prov.ListModels(ctx)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "models": models})
}

// realTestSystemPrompt / realTestUserPrompt 是 admin「实战测试」固定用的诊断 prompt。
// 故意写死成接近真实 quiz 生成的规模(几百字 system + 一段带知识点/掌握度的 user)而不
// 是用项目里真实的 QuizzerSystemPrompt——前者每次测试条件一致,便于横向比较不同中转站;
// 后者随代码迭代而变,可比性差。目的是触发和真实业务同量级的"长输出"负载,验证中转站
// 是否会在网关超时返回 502(这是 hi-code.cc 这类 Gemini 后端中转站的典型故障)。
const realTestSystemPrompt = `你是一位因材施教的学习辅导老师,负责为一名中小学生生成一套自适应练习题。

出题要求:
1. 题目必须和这节课的真实内容相关,不要凭空臆造。
2. 优先覆盖学生的弱点(掌握度低的知识点)。
3. 出 5 道题,题型混合选择题和填空题。每道题必须有题干、选项(选择题)、答案和解析。
4. 题干要自足:学生光看题干就能明白在问什么,不要用"这里""老师说的"等指代词。
5. 干扰项必须是学生真实会犯的错,不是荒谬项。

严格只输出 JSON,格式:
{
  "questions": [
    {"type":"choice","stem":"题目","options":["A","B","C","D"],"answer":1,"explanation":"解析"},
    {"type":"fill","stem":"计算:1+1=___","answer_text":["2","二"],"explanation":"解析"}
  ]
}`

const realTestUserPrompt = `请为这名学生生成这节课的练习题。

课时: 胡老师计算第一讲
科目: 数学

【学生掌握度(弱点优先)】
- 知识点片段#3: mastery=0.20(对1 错4)—— 弱点,位值原理
- 知识点片段#5: mastery=0.40(对2 错3)—— 弱点,对应思想
- 知识点片段#11: mastery=0.30(对1 错3)—— 弱点,凑十法

【课时内容摘要】
本课讲加减法巧算背后的算理。核心知识点:
1. 位值原理:数字的意义取决于它所在的位置(如 23 中的 2 在十位代表 2×10)。
2. 对应思想:比较两个加法算式时,若每个加数都更大,则和也更大。
3. 十进制(凑整):199+99+9 用凑整变成 (200-1)+(100-1)+(10-1) 简化计算。
4. 加减互逆:减法可以用加法来定义(5-2 即"几加 2 等于 5")。

请输出完整 JSON。`

// realTestAIProviderHeaders 是"实战测试"结果里回传给前端展示的响应头白名单。中转站
// 的响应头可能很多(几十个 cookie/cache-control 等),只挑对诊断后端模型有价值的几个,
// 避免结果卡片被无关头淹没。大小写不敏感匹配。
var realTestAIProviderHeaders = []string{
	"server", "via", "x-served-by", "x-upstream", "x-upstream-model",
	"x-real-model", "x-model", "x-backend", "x-provider",
	"x-ratelimit-limit-requests", "x-ratelimit-remaining-requests",
	"content-type",
}

// RealTestAIProvider 是 admin 的「实战测试」入口:和 TestAIProvider(只测连通性,
// max_tokens=5)互补——它发一个**接近真实 quiz 生成规模**的请求(max_tokens=6000、
// 几百字 prompt),直接验证中转站能否扛住真实业务的长输出负载。这一路最容易暴露中转站
// 的网关超时(典型表现:502),而这正是连不通测不出来的、只有真实长输出才会触发的故障。
//
// 同时从响应头启发式推测中转站背后的真实模型后端,展示给 admin 看(比如 hi-code.cc 实
// 际是 Gemini 后端,长输出不稳)。推测仅作参考,会一并回传原始响应头让 admin 自行判断。
//
// 不依赖已保存的 DB 行(照抄 ListAIProviderModels 模式):body 传 {base_url, api_key,
// model_name} 即测,这样配新中转站选型时不用先保存。POST /admin/api/ai/providers/test-real
func (h *adminHandler) RealTestAIProvider(c *gin.Context) {
	var req struct {
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		ModelName string `json:"model_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	if req.BaseURL == "" || req.ModelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "base_url 和 model_name 为必填"})
		return
	}

	// 90s 超时:真实 quiz step4/5 实测 46-57s,现有 Ping 的 30s 根本不够,会把正常的
	// 长输出误判成超时失败。这里给足余量,让"是中转站 502 还是本地超时"的判定更准。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	prov := ai.NewOpenAICompatProvider(req.BaseURL, req.APIKey)
	prov.SetModel(req.ModelName)

	start := time.Now()
	resp, err := prov.Chat(ctx, ai.ChatRequest{
		Temperature: 0,
		Messages: []ai.ChatMessage{
			{Role: ai.RoleSystem, Content: realTestSystemPrompt},
			{Role: ai.RoleUser, Content: realTestUserPrompt},
		},
		// 6000 直接抄 ai_service_quiz.go 的 quiz 生成 MaxTokens——要复现业务场景就必须
		// 用业务级输出上限,否则测不出真实负载。
		MaxTokens: 6000,
	})
	latency := time.Since(start).Milliseconds()

	if err != nil {
		// 失败(含 502/超时)也回传耗时——502 通常在 16-46s 出现,本地超时是 90s,
		// 耗时本身是区分两者的线索。message 直接展示给 admin,正是要诊断的信号。
		c.JSON(http.StatusOK, gin.H{
			"ok":          false,
			"message":     err.Error(),
			"latency_ms":  latency,
			"diagnosis":   realTestDiagnose(err, latency),
			"request":     realTestRequestInfo(),
		})
		return
	}

	// 成功:把诊断信号都回传——后端模型推测、关键响应头、输出采样(前 500 字)、
	// token 消耗、finish_reason。finish_reason != "stop"(如 "length"=被 max_tokens 截断)
	// 是另一个稳定性/容量信号,前端会高亮提示。
	out := gin.H{
		"ok":             true,
		"message":        "实战测试通过:中转站成功完成了一次 quiz 规模的长输出请求。",
		"latency_ms":     latency,
		"real_model_hint": realTestProbeModel(resp.Headers, resp.Content),
		"response_headers": realTestPickHeaders(resp.Headers),
		"sample_output":   realTestTruncate(resp.Content, 500),
		"usage":           resp.Usage,
		"finish_reason":   resp.FinishReason,
		"request":         realTestRequestInfo(),
	}
	c.JSON(http.StatusOK, out)
}

// realTestRequestInfo 回传本次请求的规模描述,让 admin 看清测试到底测了什么
// (prompt 多长、max_tokens 多少)。透明展示,避免"测了啥不知道"。
func realTestRequestInfo() gin.H {
	return gin.H{
		"system_prompt_chars": len(realTestSystemPrompt),
		"user_prompt_chars":   len(realTestUserPrompt),
		"max_tokens":          6000,
		"temperature":         0,
	}
}

// realTestDiagnose 对失败做一句话人话诊断。主要是区分三类常见失败:中转站 502(后端
// 超时)、本地超时(90s 到点)、其他错误(鉴权/网络)。帮 admin 快速定位是中转站问题
// 还是配置问题,而不是去看原始 error 字符串猜。
func realTestDiagnose(err error, latencyMs int64) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "502"):
		return "中转站返回 502:通常是中转站后端模型在长输出时超时断连。这正是实战测试要暴露的问题——换个中转站或换模型后端。"
	case strings.Contains(msg, "503"), strings.Contains(msg, "504"):
		return "中转站上游不可用(503/504):后端过载或网关超时,稍后再试或换中转站。"
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		return "鉴权失败(401/403):检查 api_key 是否正确、是否有该模型的权限。"
	case strings.Contains(msg, "429"):
		return "触发限流(429):中转站或后端的速率限制,降低测试频率。"
	case strings.Contains(msg, "cancelled/timed out"), strings.Contains(msg, "deadline exceeded"):
		if latencyMs >= 85000 {
			return "本地 90 秒超时:请求发出去了但模型生成太慢。这说明中转站/模型的长输出速度不达标,真实 quiz 会更糟。"
		}
		return "请求超时:" + msg
	default:
		return ""
	}
}

// realTestProbeModel 从 HTTP 响应头启发式推测中转站背后的真实模型后端。这是诊断性的、
// 尽力而为的——很多中转站不暴露后端,推测不到就如实说"未知",不强行猜。
// 同时把线索来源写进结果(如"依据:server=google"),让 admin 能自行判断可信度。
func realTestProbeModel(headers http.Header, content string) string {
	// 把所有相关头的值拼成一个小写串便于子串匹配。response id 前缀也是线索,但它在
	// body 里(content),这里一并纳入。
	lowerVals := ""
	for _, h := range realTestAIProviderHeaders {
		lowerVals += strings.ToLower(headers.Get(h)) + " "
	}
	body := strings.ToLower(content)
	// response id 如 "resp_xxx"(OpenAI)、"chatcmpl-xxx"(OpenAI 老格式)也是信号。
	lowerVals += body

	type hint struct {
		label  string
		signal string
	}
	candidates := []hint{
		{"疑似 Google Gemini", "gemini"},
		{"疑似 Google Gemini", "generativelanguage"},
		{"疑似 Google Gemini", "x-goog"},
		{"疑似 Google Gemini", "google"},
		{"疑似 Anthropic (Claude)", "anthropic"},
		{"疑似 Anthropic (Claude)", "claude"},
		{"疑似 DeepSeek", "deepseek"},
		{"疑似 Moonshot (Kimi)", "moonshot"},
		{"疑似阿里通义 (Qwen)", "qwen"},
		{"疑似百度文心", "ernie"},
		{"疑似字节豆包", "doubao"},
		{"疑似 OpenAI 系", "openai"},
		{"疑似 OpenAI 系", "chatcmpl-"},
		{"疑似 OpenAI 系", "\"resp_"},
	}
	for _, cand := range candidates {
		if strings.Contains(lowerVals, cand.signal) {
			return cand.label
		}
	}
	return "未知(中转站未在响应头/响应体暴露后端模型,无法推测)"
}

// realTestPickHeaders 从响应头里挑白名单内的头回传前端。大小写不敏感:HTTP 头名是大小
// 写不敏感的,http.Header.Get 已处理,但我们这里改写键名为小写规范化,前端展示统一。
// 空值的头跳过(不展示无意义的空头)。
func realTestPickHeaders(headers http.Header) map[string]string {
	if headers == nil {
		return nil
	}
	out := map[string]string{}
	for _, name := range realTestAIProviderHeaders {
		v := headers.Get(name)
		if v != "" {
			out[name] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// realTestTruncate 截断字符串到 max 字符,超出加省略号。响应体采样用于让 admin 一眼看到
// 模型实际输出了什么(是否完整 JSON、是否乱码、是否被截断),不需要全文。
func realTestTruncate(s string, max int) string {
	// 按 rune 计数,避免中文按字节截断出现半字。
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// GetAIStatus reports which capabilities have an enabled provider configured.
// Read-only (no building/pinging), so it's cheap and safe to call on every
// Settings page load. Used by the admin UI to show "chat: ready / embedding: 未配置".
// GET /admin/api/ai/status
func (h *adminHandler) GetAIStatus(c *gin.Context) {
	if h.aiResolver == nil {
		c.JSON(http.StatusOK, gin.H{"chat": false, "embedding": false, "rerank": false, "configured": false})
		return
	}
	chat, embed, err := h.aiResolver.IsReady()
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"chat":       chat,
		"embedding":  embed,
		"rerank":     false, // not implemented in MVP
		"configured": chat || embed,
	})
}

// --- helpers ---

// validateAIProvider checks required fields per capability. requireAPIKey is
// true only on CREATE: an update may legitimately omit the key (keep existing).
func validateAIProvider(req aiProviderDTO, requireAPIKey bool) string {
	if req.Name == "" {
		return "name 为必填"
	}
	if req.Capability != model.AICapabilityChat && req.Capability != model.AICapabilityEmbedding && req.Capability != model.AICapabilityRerank {
		return "capability 必须是 chat / embedding / rerank"
	}
	if req.ProviderType == "" {
		return "provider_type 为必填"
	}
	// Per-type required fields.
	switch req.ProviderType {
	case "openai_compat":
		if req.BaseURL == "" {
			return "openai_compat 需要填写 base_url"
		}
		if requireAPIKey && req.APIKey == "" {
			return "新建时 api_key 为必填"
		}
		if req.ModelName == "" {
			return "需要填写 model_name"
		}
	case "onnx_local":
		if req.ModelName == "" {
			return "onnx_local 需要填写 model_name(模型目录名)"
		}
	default:
		return "不支持的 provider_type: " + req.ProviderType + "(仅支持 openai_compat / onnx_local)"
	}
	return ""
}

// invalidateAI drops the cached provider for one capability, if a resolver is
// wired. Called after every provider mutation so the next resolve is fresh.
func (h *adminHandler) invalidateAI(capability string) {
	if h.aiResolver != nil {
		h.aiResolver.Invalidate(capability)
	}
}

// ---------------------------------------------------------------------------
// AI generation jobs + observability (Phase B)
// ---------------------------------------------------------------------------

// aiEnqueueRequest is the body for POST /admin/api/ai/jobs. The admin picks
// episodes from the course tree and requests a job type (segment/summary).
type aiEnqueueRequest struct {
	JobType    string `json:"job_type"`    // segment | summary
	EpisodeIDs []uint `json:"episode_ids"` // episodes to process
}

// aiEnqueueResponse reports per-episode outcomes so the admin UI can show
// "X enqueued, Y skipped (reason)" after a bulk trigger.
type aiEnqueueResponse struct {
	Enqueued []uint          `json:"enqueued"`
	Skipped  map[uint]string `json:"skipped"`
}

// EnqueueAIJobs creates segment or summary jobs for a batch of episodes.
// POST /admin/api/ai/jobs
func (h *adminHandler) EnqueueAIJobs(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	var req aiEnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	if len(req.EpisodeIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "episode_ids 不能为空"})
		return
	}
	var enqueued []uint
	var skipped map[uint]string
	var err error
	switch req.JobType {
	case "segment":
		enqueued, skipped, err = h.aiService.EnqueueSegment(req.EpisodeIDs)
	case "summary":
		enqueued, skipped, err = h.aiService.EnqueueSummary(req.EpisodeIDs)
	case "polish":
		enqueued, skipped, err = h.aiService.EnqueuePolish(req.EpisodeIDs)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "job_type 必须是 segment / summary / polish"})
		return
	}
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, aiEnqueueResponse{Enqueued: enqueued, Skipped: skipped})
}

// aiJobDTO is the admin-facing job view. Includes episode/course ids AND the
// resolved display names (episode_title/course_title/user_nickname) so the UI
// renders titles instead of bare ids. Name resolution happens in the service
// layer (see service.AIJobView); the handler just projects it to JSON.
type aiJobDTO struct {
	ID           uint     `json:"id"`
	JobType      string   `json:"job_type"`
	EpisodeID    uint     `json:"episode_id"`
	CourseID     uint     `json:"course_id"`
	Status       string   `json:"status"`
	Attempt      int      `json:"attempt"`
	Error        string   `json:"error,omitempty"`
	Progress     *float64 `json:"progress,omitempty"`
	CreatedAt    string   `json:"created_at"`
	CompletedAt  string   `json:"completed_at,omitempty"`
	EpisodeTitle string   `json:"episode_title,omitempty"`
	CourseTitle  string   `json:"course_title,omitempty"`
	UserNickname string   `json:"user_nickname,omitempty"`
}

func toAIJobDTO(v service.AIJobView) aiJobDTO {
	j := v.Job
	d := aiJobDTO{
		ID: j.ID, JobType: j.JobType,
		// EpisodeID/CourseID 在 model.AIJob 是 *uint(subject 级 advice job 为 nil)。
		// DTO 保持 uint 契约稳定(老前端读 0 不读 null),ptrVal 把 nil 转 0。
		// subject 级 job 显示 episode_id=0 / episode_title="" 正常(无对应实体)。
		EpisodeID: model.PtrVal(j.EpisodeID), CourseID: model.PtrVal(j.CourseID),
		Status: j.Status, Attempt: j.Attempt, Error: j.Error, Progress: j.Progress,
		CreatedAt:    j.CreatedAt.Format(time.RFC3339),
		EpisodeTitle: v.EpisodeTitle, CourseTitle: v.CourseTitle, UserNickname: v.UserNickname,
	}
	if j.CompletedAt != nil {
		d.CompletedAt = j.CompletedAt.Format(time.RFC3339)
	}
	return d
}

// ListAIJobs lists AI jobs, optionally filtered by job_type and/or status.
// GET /admin/api/ai/jobs?job_type=summary&status=failed
func (h *adminHandler) ListAIJobs(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, []aiJobDTO{})
		return
	}
	jobType := c.Query("job_type")
	status := c.Query("status")
	views, err := h.aiService.ListJobs(jobType, status, 100)
	if err != nil {
		respondError(c, err)
		return
	}
	out := make([]aiJobDTO, 0, len(views))
	for _, v := range views {
		out = append(out, toAIJobDTO(v))
	}
	// Include stats so the UI can show counts without a second request.
	stats, _ := h.aiService.JobStats()
	c.JSON(http.StatusOK, gin.H{"jobs": out, "stats": stats})
}

// GetAIJob returns one job (for a detail view).
// GET /admin/api/ai/jobs/:id
func (h *adminHandler) GetAIJob(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	view, err := h.aiService.GetJob(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if view == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job 不存在"})
		return
	}
	// Include the decision runs for this job so the detail view can replay them.
	// Enriched 携带 episode/course/user 标题,让详情页的 runs 列表也能看到"在哪节课"。
	runs, _ := h.aiService.ListRunsForJobEnriched(id)
	c.JSON(http.StatusOK, gin.H{"job": toAIJobDTO(*view), "runs": runs})
}

// ListAIRuns returns recent decision runs (across all jobs), newest first.
// GET /admin/api/ai/runs?limit=50
// This powers the "agent decision trace" panel — the observability centerpiece.
// 返回 AIRunView(带 episode_title/course_title/user_nickname),让决策痕迹表
// 和 Dashboard 最近活动能展示课程/课时,不只是 capability + #job_id。
func (h *adminHandler) ListAIRuns(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, []service.AIRunView{})
		return
	}
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := h.aiService.ListRecentRunsEnriched(limit)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, runs)
}

// GetAIRun returns one decision run's full detail (prompt/response/usage).
// GET /admin/api/ai/runs/:id
func (h *adminHandler) GetAIRun(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	run, err := h.aiService.GetRun(id)
	if err != nil {
		respondError(c, err)
		return
	}
	if run == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run 不存在"})
		return
	}
	c.JSON(http.StatusOK, run)
}

// ResetAIJob manually resets one 'processing' AI job back to 'queued' on admin
// demand — the manual counterpart of the automatic reaper. Use case: the worker
// is alive but a relay call hung without crashing, so the job isn't stale
// enough for the 30min reaper yet, but the admin has judged it stuck. Clears
// claimed_at + error so the next worker poll re-claims it cleanly.
// POST /admin/api/ai/jobs/:id/reset
func (h *adminHandler) ResetAIJob(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := h.aiService.ResetJob(id); err != nil {
		// ErrJobNotProcessing is non-fatal: the job already finished or was
		// reaped. Surface 409 so the UI can say "nothing to reset" rather than
		// silently pretending success (which would hide a double-reset).
		if err == repository.ErrJobNotProcessing {
			c.JSON(http.StatusConflict, gin.H{"error": "任务不在处理中(可能已完成或已被重置)"})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RetryAIJob revives one 'failed' AI job back to 'queued' so the worker re-runs
// it. Use case: a job failed (e.g. embedding/chat provider was misconfigured),
// the admin fixed the underlying problem, now they want to re-run instead of
// leaving it failed forever. This is the ONLY way to revive a failed job —
// failJob marks jobs failed without auto-retry (AI calls cost money, so we don't
// loop on a bad config). Clears error + claimed_at; the next worker poll
// (3s) re-claims it.
// POST /admin/api/ai/jobs/:id/retry
func (h *adminHandler) RetryAIJob(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := h.aiService.RetryJob(id); err != nil {
		// ErrJobNotFailed is non-fatal: the job isn't failed (it succeeded, is
		// queued/processing, or was already retried). Surface 409 so the UI can
		// say "nothing to retry" rather than silently pretending success.
		if err == repository.ErrJobNotFailed {
			c.JSON(http.StatusConflict, gin.H{"error": "任务不是失败状态(可能已成功或已被重试)"})
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// SkipPolishAIJob is the polish-specific escape hatch. A failed polish job
// HALTS the downstream chain (segment never auto-enqueues). When the admin
// decides polish isn't worth fixing (raw subtitle is good enough, or the
// provider issue can't be resolved), this endpoint marks the job done and
// chains segment so AI proceeds off the raw text. Only valid on a FAILED
// POLISH job — other states/types return 409 so the UI can hide the button.
// POST /admin/api/ai/jobs/:id/skip-polish
func (h *adminHandler) SkipPolishAIJob(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 id"})
		return
	}
	if err := h.aiService.SkipPolish(id); err != nil {
		switch err {
		case repository.ErrJobNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		case repository.ErrJobNotPolish:
			c.JSON(http.StatusConflict, gin.H{"error": "该任务不是 polish 任务"})
		case repository.ErrJobNotFailed:
			c.JSON(http.StatusConflict, gin.H{"error": "任务不是失败状态,无需跳过"})
		default:
			respondError(c, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------------------------------------------------------------------
// Phase C — quiz observability (admin): per-user quizzes + detail + summaries
// ---------------------------------------------------------------------------

// GetAISummary serves a generated summary's content to the admin. The AI
// Workflow job view links a summary job to its episode; this endpoint lets the
// admin read the actual headline/key_points/concepts without switching to the
// client. GET /admin/api/ai/summaries/:episodeID
func (h *adminHandler) GetAISummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI 子系统未配置"})
		return
	}
	episodeID, err := parseUintParam(c, "episodeID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 episodeID"})
		return
	}
	summary, err := h.aiService.GetSummary(episodeID)
	if err != nil {
		respondError(c, err)
		return
	}
	if summary == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "该课时暂无总结"})
		return
	}
	// Return the raw row; the admin SPA parses summary_json for rich display.
	c.JSON(http.StatusOK, gin.H{
		"episode_id":   summary.EpisodeID,
		"course_id":    summary.CourseID,
		"summary_json": summary.SummaryJSON,
		"model_used":   summary.ModelUsed,
		"created_at":   summary.CreatedAt.Format("2006-01-02 15:04:05"),
	})
}

// ListUserQuizzes lists all of a user's quizzes (the per-user AI view entry).
// GET /admin/api/ai/users/:userID/quizzes
func (h *adminHandler) ListUserQuizzes(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	quizzes, err := h.aiService.ListQuizzesForUser(userID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, quizzes)
}

// GetQuizDetail returns the full per-quiz observability bundle: questions WITH
// answers, the student's answer history, their mastery, the agent's feedback,
// and the ai_runs that produced it (trace_json lives on the runs — the SPA
// renders the "思考时间线" from it). GET /admin/api/ai/quizzes/:quizID
func (h *adminHandler) GetQuizDetail(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "AI 子系统未配置"})
		return
	}
	quizID, err := parseUintParam(c, "quizID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 quizID"})
		return
	}
	detail, err := h.aiService.GetQuizDetail(quizID)
	if err != nil {
		respondError(c, err)
		return
	}
	if detail == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "题库不存在"})
		return
	}
	c.JSON(http.StatusOK, detail)
}

// ---------------------------------------------------------------------------
// Phase D — admin 课程级总结(course-unique 纯内容总结,agent 驱动)
// ---------------------------------------------------------------------------

// courseSummaryAdminDTO 是 ai_course_summary 的 admin JSON 视图。status 让前端区分:
//   - ready:有总结(summary_text 字段非空)
//   - generating:无总结 + 有在途 job(前端轮询)
//   - 空 status + 无 summary:无总结也未生成(前端显示"生成总结"按钮)
//
// EpisodeCountAtGen / CurrentEpisodeCount 用于陈旧检测:前者是生成时快照的"已总结
// 课时数"(存 DB),后者是读时现算(每次 GET 查 ai_summaries.count)。差值 > 0 = 陈旧。
type courseSummaryAdminDTO struct {
	Status             string `json:"status"` // ready | generating | ""(无总结未生成)
	SummaryText        string `json:"summary_text,omitempty"`
	ModelUsed          string `json:"model_used,omitempty"`
	GeneratedAt        string `json:"generated_at,omitempty"`
	EpisodeCountAtGen  int    `json:"episode_count_at_gen,omitempty"`
	CurrentEpisodeCount int   `json:"current_episode_count,omitempty"`
}

// TriggerCourseSummary 触发为某课程生成课程级总结(异步入队 course_summary job)。
// 返回 status="generating"(或 unavailable,当 AI off 或课程不存在)。前端随后轮询 GET
// 端点直到 ready。
// POST /admin/api/ai/courses/:id/course-summary
//
// 设计为"强制重生成"语义:即使已有总结,POST 也会重跑(覆盖)。这让 admin 能刷新过期
// 总结(比如课程新增了 episode 之后)。去重靠 service 的在途 job 检查(避免连点堆 job)。
func (h *adminHandler) TriggerCourseSummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	status, err := h.aiService.EnqueueCourseSummary(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// GetCourseSummary 取某课程的最新课程总结(供 admin GET 端点)。
// GET /admin/api/ai/courses/:id/course-summary
//
// 响应 status 三态:
//   - ready:有总结(返回 summary_text + 元数据)
//   - generating:无总结 + 有在途 job(前端继续轮询 / 显示 spinner)
//   - "":无总结 + 无在途 job(前端显示"生成总结"按钮)
func (h *adminHandler) GetCourseSummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	summary, err := h.aiService.GetCourseSummary(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	dto := courseSummaryAdminDTO{}
	// CurrentEpisodeCount 无论有没有 summary 都算——前端 ready 时用它跟 at_gen
	// 比对(陈旧提示),无 summary 时无害(前端不显示这个字段)。
	if currentCount, cerr := h.aiService.CountEpisodesWithSummary(courseID); cerr == nil {
		dto.CurrentEpisodeCount = int(currentCount)
	}
	if summary != nil {
		dto.Status = "ready"
		dto.SummaryText = summary.SummaryText
		dto.ModelUsed = summary.ModelUsed
		dto.GeneratedAt = summary.GeneratedAt.Format(time.RFC3339)
		dto.EpisodeCountAtGen = summary.EpisodeCountAtGen
	} else if h.aiService.HasPendingCourseSummaryJob(courseID) {
		// 无总结但正在生成——前端据此显示 spinner 并继续轮询。
		dto.Status = "generating"
	}
	c.JSON(http.StatusOK, dto)
}

// ListEpisodeSummaryStatus 返回某课程下"已有 AI summary"的 episode id 列表。
// GET /admin/api/ai/courses/:id/summaries-status
//
// 给 AI 控制台「内容管理」tab gate 每集"删除"按钮用:没有 summary 的课时不应显示
// 删除按钮(删了也是无意义的幂等 no-op,反而误导 admin)。返回一个 id 数组,前端转 Set。
func (h *adminHandler) ListEpisodeSummaryStatus(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, gin.H{"episode_ids_with_summary": []uint{}})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	ids, err := h.aiService.ListEpisodeSummaryStatus(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"episode_ids_with_summary": ids})
}

// ---------------------------------------------------------------------------
// Phase E — admin 用户学习报告(agent 驱动,跨课程画像)
// ---------------------------------------------------------------------------

// userStudyReportDTO 是 user_study_report 的 admin JSON 视图。status 让前端区分:
//   - ready:有报告(report 字段非空)
//   - generating:无报告 + 有在途 job(前端轮询)
//   - 空 status + 无 report:无报告也未生成(前端显示"生成报告"按钮)
type userStudyReportDTO struct {
	Status      string `json:"status"`           // ready | generating | ""(无报告未生成)
	Report      string `json:"report,omitempty"` // 报告文本(ready 时有)
	ModelUsed   string `json:"model_used,omitempty"`
	GeneratedAt string `json:"generated_at,omitempty"`
}

// TriggerUserStudyReport 触发为某用户生成学习报告(异步入队 user_report job)。
// 返回 status="generating"(或 unavailable,当 AI off)。前端随后轮询 GET 端点直到 ready。
// POST /admin/api/ai/users/:id/study-report
//
// 设计为"强制重生成"语义:即使已有报告,POST 也会重跑(覆盖)。这让 admin 能刷新过期
// 报告。去重靠 service 的在途 job 检查(避免连点堆 job)。
func (h *adminHandler) TriggerUserStudyReport(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的用户 id"})
		return
	}
	status, err := h.aiService.EnqueueUserReport(userID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// GetUserStudyReport 取某用户的最新学习报告(供 admin GET 端点)。
// GET /admin/api/ai/users/:id/study-report
//
// 响应 status 三态:
//   - ready:有报告(返回 report 文本 + 元数据)
//   - generating:无报告 + 有在途 job(前端继续轮询 / 显示 spinner)
//   - "":无报告 + 无在途 job(前端显示"生成报告"按钮)
func (h *adminHandler) GetUserStudyReport(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的用户 id"})
		return
	}
	report, err := h.aiService.GetUserStudyReport(userID)
	if err != nil {
		respondError(c, err)
		return
	}
	dto := userStudyReportDTO{}
	if report != nil {
		dto.Status = "ready"
		dto.Report = report.ReportText
		dto.ModelUsed = report.ModelUsed
		dto.GeneratedAt = report.GeneratedAt.Format(time.RFC3339)
	} else if h.aiService.HasPendingUserReportJob(userID) {
		// 无报告但正在生成——前端据此显示 spinner 并继续轮询。
		dto.Status = "generating"
	}
	c.JSON(http.StatusOK, dto)
}

// ---------------------------------------------------------------------------
// Prompt 预览(admin 调优 hint 时实时看效果)
// ---------------------------------------------------------------------------

// previewPromptRequest 是 POST /admin/api/ai/courses/:id/preview-prompt 的 body。
// agent 三选一,对应 summary / quiz / advice 三个 agent 的 prompt 构造。
type previewPromptRequest struct {
	Agent string `json:"agent"` // summary | quiz | advice
}

// resolvedHintsDTO 展示"这个课程最终解析出的 5 个 hint 来源"。让 admin 直观看到
// 当前用的是学科默认还是课程覆盖的(Effective*Hint 会从课程级回退到学科级)。
type resolvedHintsDTO struct {
	WhisperHint string `json:"whisper_hint"`
	SummaryHint string `json:"summary_hint"`
	QuizHint    string `json:"quiz_hint"`
	AdviceHint  string `json:"advice_hint"`
	TermDict    string `json:"term_dict"`
}

// previewPromptResponse 是预览端点的完整响应。system_prompt + user_prompt 就是
// 这个课程+agent 最终会发给 LLM 的开场消息(预览即真相);resolved_hints 帮 admin
// 判断"我改的 hint 真的生效了吗"。
type previewPromptResponse struct {
	SystemPrompt  string           `json:"system_prompt"`
	UserPrompt    string           `json:"user_prompt"`
	ResolvedHints resolvedHintsDTO `json:"resolved_hints"`
}

// PreviewCoursePrompt 拼出某课程 + 某 agent 最终会发给 LLM 的完整 prompt,**不调 LLM**。
// 纯文本拼接,响应快(<10ms)。用于 admin 调优 hint 后立刻看效果,不用等真生成。
//
// POST /admin/api/ai/courses/:id/preview-prompt  body: {"agent": "summary"|"quiz"|"advice"}
//
// 工作流:
//  1. 加载 course + 预加载 subject(courseRepo.FindByID 不预加载 Subject,这里单独取一次)。
//  2. 用 Course.EffectiveXxxHint(subject) 解析出最终生效的 5 个 hint(课程级覆盖学科级)。
//  3. 根据 agent 类型构造对应 Request(填入 hint/TermDict,episode/user/mastery 等运行时字段
//     填占位值——预览不针对具体学生,重点看 hint/TermDict 的解析结果),调 agent 包的导出
//     preview 函数拿到 (system, user)。
//  4. 返回 system_prompt + user_prompt + resolved_hints。
func (h *adminHandler) PreviewCoursePrompt(c *gin.Context) {
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	var req previewPromptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效"})
		return
	}
	// 校验 agent 字段(白名单),防 injection / 笔误。
	switch req.Agent {
	case "summary", "quiz", "advice":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "agent 必须是 summary / quiz / advice"})
		return
	}

	course, err := h.courseRepo.FindByID(courseID)
	if err != nil {
		respondError(c, err)
		return
	}
	if course == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "课程不存在"})
		return
	}
	// courseRepo.FindByID 不预加载 Subject,单独取一次。subject 可能被删(course 仍残留
	// 旧 SubjectID),取不到时退化为零值 Subject(hint 全空,prompt 仍可拼出来,只是没 hint 注入)。
	var subject model.Subject
	if course.SubjectID != 0 {
		if subj, _ := h.subjectRepo.FindByID(course.SubjectID); subj != nil {
			subject = *subj
		}
	}

	// 解析最终生效的 5 个 hint(课程级覆盖学科级)。展示给 admin 看"现在生效的是哪个值"。
	resolved := resolvedHintsDTO{
		WhisperHint: course.EffectiveWhisperHint(subject),
		SummaryHint: course.EffectiveSummaryHint(subject),
		QuizHint:    course.EffectiveQuizHint(subject),
		AdviceHint:  course.EffectiveAdviceHint(subject),
		TermDict:    course.EffectiveTermDict(subject),
	}

	// 取课程下任意一集填进 Request 的 EpisodeID/EpisodeTitle 等字段。
	// 预览不针对具体学生/课时,但 prompt 模板需要这些占位字段(如 quiz prompt 显示"课时: xxx")。
	// 没有课时就填占位值,prompt 仍能拼出来(只是显示"#0"或空标题)。
	var episodeID uint
	var episodeTitle string
	if h.episodeRepo != nil {
		if eps, eErr := h.episodeRepo.ListByCourse(courseID); eErr == nil && len(eps) > 0 {
			episodeID = eps[0].ID
			episodeTitle = eps[0].Title
		}
	}
	subjectLabel := subject.Label // 学科中文名(如"数学"),prompt 里用

	var systemPrompt, userPrompt string
	switch req.Agent {
	case "summary":
		// summary 不需要 episode/user 信息,只需 Subject + SummaryHint + TermDict + Chunks。
		// Chunks 留空(prompt 显示空字幕段);admin 看的是 hint/TermDict 的注入位置和效果。
		systemPrompt, userPrompt = agent.BuildSummaryPromptForPreview(agent.SummarizerRequest{
			CourseID:    courseID,
			Subject:     subjectLabel,
			SummaryHint: resolved.SummaryHint,
			TermDict:    resolved.TermDict,
		})
	case "quiz":
		// quiz 的 user prompt 需要 EpisodeTitle/Subject 作为上下文;mastery 留空
		// (预览不针对具体学生,prompt 会显示"新学生,暂无答题记录")。
		systemPrompt, userPrompt = agent.BuildQuizPromptForPreview(agent.QuizzerRequest{
			EpisodeID:    episodeID,
			CourseID:     courseID,
			EpisodeTitle: episodeTitle,
			Subject:      subjectLabel,
		})
	case "advice":
		// advice 选 course scope(和课程级 preview 最匹配)。填 Subject + AdviceHint + TermDict;
		// mastery/ExtraContext 留空(prompt 显示"当前无答题记录")。
		systemPrompt, userPrompt = agent.BuildAdvicePromptForPreview(agent.AdviceRequest{
			Scope:      agent.ScopeCourse,
			ScopeID:    courseID,
			ScopeTitle: course.Title,
			Subject:    subjectLabel,
			CourseID:   courseID,
			AdviceHint: resolved.AdviceHint,
			TermDict:   resolved.TermDict,
		})
	}

	c.JSON(http.StatusOK, previewPromptResponse{
		SystemPrompt:  systemPrompt,
		UserPrompt:    userPrompt,
		ResolvedHints: resolved,
	})
}

// ---------------------------------------------------------------------------
// 重新生成 + 删除(2026-07-19 加):AI 控制台中枢的后端端点。
// ---------------------------------------------------------------------------

// regenerateUserQuizRequest 是 POST /admin/api/ai/users/:userID/quizzes/regenerate 的 body。
type regenerateUserQuizRequest struct {
	EpisodeID uint `json:"episode_id" binding:"required"`
}

// RegenerateUserQuiz 给某学生重出一套某 episode 的题(archive 旧 active quiz,插新 active)。
// POST /admin/api/ai/users/:userID/quizzes/regenerate  body: {"episode_id":123}
// 返回 {status: "generating" | "unavailable"}(对齐客户端 RegenerateQuiz 语义)。
func (h *adminHandler) RegenerateUserQuiz(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	var req regenerateUserQuizRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效(需要 episode_id)"})
		return
	}
	status, err := h.aiService.RegenerateQuizForUser(userID, req.EpisodeID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// regenerateUserAdviceRequest 是 POST /admin/api/ai/users/:userID/advice/regenerate 的 body。
type regenerateUserAdviceRequest struct {
	Scope   string `json:"scope" binding:"required"`             // episode | course | subject
	ScopeID uint   `json:"scope_id" binding:"required"`          // 对应实体 id
}

// RegenerateUserAdvice 强制重生成某 (user, scope, scopeID) 的 advice(覆盖旧记录)。
// 三档 scope 都支持;这是 course/subject 级 advice 的唯一刷新入口。
// POST /admin/api/ai/users/:userID/advice/regenerate  body: {"scope":"course","scope_id":5}
func (h *adminHandler) RegenerateUserAdvice(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	var req regenerateUserAdviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式无效(需要 scope + scope_id)"})
		return
	}
	if req.Scope != "episode" && req.Scope != "course" && req.Scope != "subject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope 必须是 episode / course / subject"})
		return
	}
	status, err := h.aiService.RegenerateAdvice(userID, req.Scope, req.ScopeID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// ListUserAdvice 列出某用户的所有 advice(三档 scope,所有 scope_id),按 generated_at DESC。
// GET /admin/api/ai/users/:userID/advice
// 给 AI 控制台"这个学生有哪些 advice + 删除按钮"用。
func (h *adminHandler) ListUserAdvice(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	rows, err := h.aiService.ListUserAdvice(userID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// DeleteAISummary 删除某 episode 的 summary(物理删,覆盖式重新生成的对照操作)。
// DELETE /admin/api/ai/summaries/:episodeID
func (h *adminHandler) DeleteAISummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	episodeID, err := parseUintParam(c, "episodeID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 episodeID"})
		return
	}
	if err := h.aiService.DeleteSummary(episodeID); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteAIQuiz 删除一条 quiz(物理删,Fk CASCADE 自动清 Question + Answer)。
// DELETE /admin/api/ai/quizzes/:quizID
// 注意:这和 archive 不同 —— archive 保留历史(只翻 status='archived'),delete 彻底清除。
func (h *adminHandler) DeleteAIQuiz(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	quizID, err := parseUintParam(c, "quizID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 quizID"})
		return
	}
	if err := h.aiService.DeleteQuiz(quizID); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteUserAdvice 删除某 (user, scope, scope_id) 的 advice。scope/scope_id 从 query 取
// (DELETE body 用得不普遍,且语义上是"删除某条",query 参数更直观)。
// DELETE /admin/api/ai/users/:userID/advice?scope=episode&scope_id=123
func (h *adminHandler) DeleteUserAdvice(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	scope := c.Query("scope")
	if scope != "episode" && scope != "course" && scope != "subject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope 参数必须是 episode / course / subject"})
		return
	}
	scopeIDStr := c.Query("scope_id")
	scopeID, err := strconv.ParseUint(scopeIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope_id 参数无效"})
		return
	}
	if err := h.aiService.DeleteAdvice(userID, scope, uint(scopeID)); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteCourseSummary 删除某课程的总结。
// DELETE /admin/api/ai/courses/:id/course-summary
func (h *adminHandler) DeleteCourseSummary(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	courseID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的课程 id"})
		return
	}
	if err := h.aiService.DeleteCourseSummary(courseID); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteUserStudyReport 删除某用户的学习报告。
// DELETE /admin/api/ai/users/:userID/study-report
func (h *adminHandler) DeleteUserStudyReport(c *gin.Context) {
	if h.aiService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 子系统未配置"})
		return
	}
	userID, err := parseUintParam(c, "userID")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 userID"})
		return
	}
	if err := h.aiService.DeleteUserReport(userID); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
