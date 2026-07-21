package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"github.com/gin-gonic/gin"
	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/model"
)

// Code split from admin_ai.go for navigability.

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
	if !bindJSON(c, &req) { return }
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
	if !bindJSON(c, &req) { return }
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
	if !bindJSON(c, &req) { return }
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
	if !bindJSON(c, &req) { return }
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
