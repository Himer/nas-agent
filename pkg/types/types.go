// Package types 定义 nas-agent 的核心接口和数据结构。
//
// Go 用 interface 实现"鸭子类型"——只要实现了接口里的全部方法，
// 就可以被当作该类型使用。
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

// Action 是 Agent 解析后准备执行的一次工具调用。
//
// Tool 区分工具类型：
//   - "bash"       执行 shell 命令，命令在 Command 字段
//   - "todo_write" 更新任务清单，清单在 Todos 字段
//
// 兼容旧逻辑：Tool 为空时按 "bash" 处理。
type Action struct {
	Tool       string
	Command    string
	Todos      []TodoItem
	ToolCallID string // 来自 ToolCall.ID，回填到 tool result 消息里
}

// TodoItem 是 todo_write 工具中的一条任务。
// Status 取值：pending / in_progress / completed。
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
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
	// Execute 执行单条命令并返回结果。每次调用都是独立子进程。
	Execute(ctx context.Context, action Action) ExecResult
}

// Agent 是控制循环的抽象。
type Agent interface {
	Run(ctx context.Context, task string) error
	Messages() []Message
}
