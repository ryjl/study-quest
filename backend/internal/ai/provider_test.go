package ai

import (
	"encoding/json"
	"testing"
)

// TestJSONSchemaResponseFormat 钉住构造出的 response_format 形状符合 OpenAI
// "structured outputs" 规范(strict json_schema),供未来换支持约束解码的后端时调用方
// 不因形状错误踩坑。当前后端不支持 response_format(探测 400),此函数暂无业务调用方,
// 此测试是它唯一的"用法契约",防止以后改坏。
func TestJSONSchemaResponseFormat(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"desc":{"type":"string"}},"required":["desc"],"additionalProperties":false}`)
	got, err := JSONSchemaResponseFormat("probe", schema)
	if err != nil {
		t.Fatal(err)
	}

	// 形状应是 {"type":"json_schema","json_schema":{"name":"probe","strict":true,"schema":{...}}}
	var parsed struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Strict bool            `json:"strict"`
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("产物不是合法 JSON: %v\nraw: %s", err, got)
	}
	if parsed.Type != "json_schema" {
		t.Errorf("type: 期望 json_schema, 实际 %q", parsed.Type)
	}
	if parsed.JSONSchema.Name != "probe" {
		t.Errorf("name: 期望 probe, 实际 %q", parsed.JSONSchema.Name)
	}
	if !parsed.JSONSchema.Strict {
		t.Error("strict 必须为 true(约束解码的开关)")
	}
	// schema 原样透传
	if string(parsed.JSONSchema.Schema) != string(schema) {
		t.Errorf("schema 未原样透传:\n got: %s\nwant: %s", parsed.JSONSchema.Schema, schema)
	}
}

// TestChatRequest_ResponseFormatOmitEmpty 钉住:不设 ResponseFormat 时,marshal 出的
// 请求体不含 response_format 字段(omitempty 生效)——这是当前所有生成点的状态,保证
// 加这个字段对现有后端零影响(不会因为多个空字段触发 unsupported_parameter)。
func TestChatRequest_ResponseFormatOmitEmpty(t *testing.T) {
	// 不设 ResponseFormat
	req := ChatRequest{Model: "llmy", Messages: []ChatMessage{{Role: RoleUser, Content: "hi"}}}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "" {
		t.Fatal("marshal 产物为空")
	}
	// 反序列化回 map,确认没有 response_format 键
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["response_format"]; ok {
		t.Errorf("不设 ResponseFormat 时不应出现 response_format 字段(omitempty 失效?): %s", b)
	}

	// 设了 ResponseFormat 后字段出现
	rf, _ := JSONSchemaResponseFormat("x", json.RawMessage(`{}`))
	req.ResponseFormat = rf
	b2, _ := json.Marshal(req)
	var m2 map[string]any
	json.Unmarshal(b2, &m2)
	if _, ok := m2["response_format"]; !ok {
		t.Errorf("设了 ResponseFormat 后应出现 response_format 字段: %s", b2)
	}
}
