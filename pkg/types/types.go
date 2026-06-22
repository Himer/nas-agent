// Package types 定义 mini-agent 的核心接口和数据结构。
//
// 对应 Python 版 minisweagent/__init__.py 里的 Protocol 定义。
// Go 用 interface 实现"鸭子类型"，效果与 Python Protocol 完全一致：
// 只要实现了接口里的全部方法，就可以被当作该类型使用。
package types

import "context"

// Message 表示一条聊天消息。直接对应 OpenAI Chat Completions API 的消息格式。
//
// Role 取值：
//   - "system"    系统提示
//   - "user"      用户消息（也用于把命令执行结果反馈给 AI）
//   - "assistant" AI 回复
//   - "tool"      工具调用结果（OpenAI tool-calling 模式）
//   - "exit"      内部使用，标记 Agent 循环结束
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`

	// Extra 用于在 Agent 内部携带额外数据（例如解析出的命令、退出原因），
	// 序列化到 OpenAI API 时会被剥离。
	Extra map[string]any `json:"-"`
}

// ToolCall 对应 OpenAI tool-calling 协议中的一次工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // 永远是 "function"
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 字符串
}

// Action 是 Agent 解析后准备执行的命令。
type Action struct {
	Command    string
	ToolCallID string // 来自 ToolCall.ID，回填到 tool result 消息里
}

// ExecResult 是 Environment.Execute 的返回结果。
type ExecResult struct {
	Output     string
	ReturnCode int
	Exception  string // 非空表示发生了 Go 层面的异常（超时等）
}

// Model 是大语言模型的抽象。
//
// 任何能"接收聊天历史，返回带命令的回复"的对象都能成为 Model。
type Model interface {
	// Query 把全部历史发给大模型，返回 AI 的回复（assistant 消息）。
	// 解析出的命令放在返回 Message 的 Extra["actions"] 里。
	Query(ctx context.Context, messages []Message) (Message, error)
}

// Environment 是命令执行环境的抽象。
//
// 默认实现是本地 shell；可以替换为 Docker、远程沙盒等。
type Environment interface {
	// Execute 执行单条命令并返回结果。每次调用都是独立子进程（与 Python 版一致）。
	Execute(ctx context.Context, action Action) ExecResult
}

// Agent 是控制循环的抽象。
type Agent interface {
	Run(ctx context.Context, task string) error
	Messages() []Message
}
