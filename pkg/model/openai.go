// Package model 提供大语言模型实现。
//
// 对应 Python 版 minisweagent/models/litellm_model.py。
// 这里不依赖 litellm，直接走 OpenAI 兼容的 /chat/completions 接口，
// 因此可以接 OpenAI、DeepSeek、Qwen、Moonshot、本地 vLLM 等任何兼容服务。
package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Himer/mini-agent/pkg/types"
)

// BashTool 是开放给大模型的唯一工具：执行 bash 命令。
// 与 Python 版 BASH_TOOL 字段一致。
var BashTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "bash",
		"description": "Execute a bash (or PowerShell on Windows) command on the user's machine.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute.",
				},
			},
			"required": []string{"command"},
		},
	},
}

// OpenAI 是一个 OpenAI 兼容的 Chat Completions 客户端。
type OpenAI struct {
	APIKey     string
	BaseURL    string // 例如 https://api.openai.com/v1 或 https://api.deepseek.com/v1
	ModelName  string
	HTTPClient *http.Client
}

// NewOpenAI 构造一个客户端。BaseURL 为空时默认 https://api.openai.com/v1。
func NewOpenAI(apiKey, baseURL, modelName string) *OpenAI {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAI{
		APIKey:     apiKey,
		BaseURL:    strings.TrimRight(baseURL, "/"),
		ModelName:  modelName,
		HTTPClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

// chatRequest / chatResponse 仅声明我们关心的字段，其它字段由 JSON 解码忽略。
type chatRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Tools    []map[string]any `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      types.Message `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// normalizeMessages 把内部 Message 列表转换成符合 OpenAI 兼容 API 要求的最简 JSON 结构。
//
// 各家服务（OpenAI / DeepSeek / 智谱 GLM / Moonshot 等）对消息字段的接受度不同，
// 这里强制满足最严格的智谱版规则，避免出现 "messages 参数非法 (400001)"：
//   - 任何消息都必须显式带 content 字段（即使为空字符串）；
//   - assistant 带 tool_calls 时也保留 content（""）；
//   - tool 消息必须有 tool_call_id 且 content 非空（空则填占位符）；
//   - 不发送内部使用的 Extra 等字段。
func normalizeMessages(in []types.Message) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, m := range in {
		entry := map[string]any{
			"role":    m.Role,
			"content": m.Content, // 显式保留，哪怕是 ""
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				typ := tc.Type
				if typ == "" {
					typ = "function"
				}
				calls = append(calls, map[string]any{
					"id":   tc.ID,
					"type": typ,
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				})
			}
			entry["tool_calls"] = calls
		}
		if m.Role == "tool" {
			entry["tool_call_id"] = m.ToolCallID
			if m.Content == "" {
				entry["content"] = "(empty)"
			}
		}
		out = append(out, entry)
	}
	return out
}

// Query 实现 types.Model：把消息历史发给大模型，返回 assistant 回复。
//
// 解析逻辑：
//  1. 优先从 tool_calls 中提取 bash 命令（OpenAI 标准 tool-calling）
//  2. 若模型不支持 tool-calling，则从 content 中按代码块兜底提取
//  3. 解析出的命令放在 message.Extra["actions"] 中供 Agent 使用
func (o *OpenAI) Query(ctx context.Context, messages []types.Message) (types.Message, error) {
	reqBody := chatRequest{
		Model:    o.ModelName,
		Messages: normalizeMessages(messages),
		Tools:    []map[string]any{BashTool},
	}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", o.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return types.Message{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.APIKey)

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return types.Message{}, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return types.Message{}, fmt.Errorf("api error %d: %s", resp.StatusCode, string(raw))
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return types.Message{}, fmt.Errorf("decode response: %w; body=%s", err, string(raw))
	}
	if parsed.Error != nil {
		return types.Message{}, fmt.Errorf("api error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return types.Message{}, errors.New("empty choices in response")
	}

	msg := parsed.Choices[0].Message
	msg.Role = "assistant" // 兜底
	actions, err := extractActions(msg)
	if err != nil {
		return msg, err
	}
	if msg.Extra == nil {
		msg.Extra = map[string]any{}
	}
	msg.Extra["actions"] = actions
	msg.Extra["finish_reason"] = parsed.Choices[0].FinishReason
	return msg, nil
}

// extractActions 从 assistant 消息中解析出待执行命令。
//
// 优先级：tool_calls > 文本中的 ```bash``` 代码块。
func extractActions(msg types.Message) ([]types.Action, error) {
	if len(msg.ToolCalls) > 0 {
		actions := make([]types.Action, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if tc.Function.Name != "bash" {
				return nil, fmt.Errorf("unknown tool %q", tc.Function.Name)
			}
			var args struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf("parse tool arguments: %w", err)
			}
			if args.Command == "" {
				return nil, errors.New("empty 'command' in tool call")
			}
			actions = append(actions, types.Action{Command: args.Command, ToolCallID: tc.ID})
		}
		return actions, nil
	}
	// 兜底：从 ```bash ... ``` 代码块抽取（兼容不支持 tool calling 的模型）
	if cmd := extractCodeBlock(msg.Content); cmd != "" {
		return []types.Action{{Command: cmd}}, nil
	}
	return nil, errors.New("no tool_calls and no fenced code block found in assistant response")
}

// extractCodeBlock 从 markdown 文本里抽出第一个 ```...``` 代码块的内容。
func extractCodeBlock(text string) string {
	start := strings.Index(text, "```")
	if start < 0 {
		return ""
	}
	rest := text[start+3:]
	// 跳过语言标识符（如 bash, sh, mswea_bash_command）直到换行
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	}
	end := strings.Index(rest, "```")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}
