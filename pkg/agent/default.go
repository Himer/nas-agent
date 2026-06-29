// Package agent 实现 Agent 控制循环。
//
// 整个 Agent 的灵魂就是一个 for 循环：问模型 → 执行命令 → 把结果加回历史 → 重复。
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Himer/nas-agent/pkg/config"
	"github.com/Himer/nas-agent/pkg/environment"
	"github.com/Himer/nas-agent/pkg/types"
)

// ANSI 颜色（Windows 10+ 终端原生支持）。
const (
	cReset  = "\x1b[0m"
	cDim    = "\x1b[2m"
	cBold   = "\x1b[1m"
	cCyan   = "\x1b[36m"
	cGreen  = "\x1b[32m"
	cYellow = "\x1b[33m"
	cRed    = "\x1b[31m"
	cBlue   = "\x1b[34m"
	cMag    = "\x1b[35m"
)

// Default 是默认 Agent 实现。
type Default struct {
	Model     types.Model
	Env       types.Environment
	StepLimit int  // 0 表示不限
	Confirm   bool // true: 每条命令都问用户是否执行（skip_confirm 关闭时启用）

	messages []Message        // 内部消息历史
	todos    []types.TodoItem // 当前任务清单（由 todo_write 工具维护）
}

// Message 是 types.Message 的别名，单纯为了让 Agent 包对外暴露的 API 更短。
type Message = types.Message

// New 创建一个 Agent。
func New(model types.Model, env types.Environment, stepLimit int, confirm bool) *Default {
	return &Default{Model: model, Env: env, StepLimit: stepLimit, Confirm: confirm}
}

// Messages 返回当前完整聊天历史（拷贝）。
func (a *Default) Messages() []types.Message {
	out := make([]types.Message, len(a.messages))
	copy(out, a.messages)
	return out
}

// Run 启动 Agent 主循环，直到任务完成或达到步数上限。
func (a *Default) Run(ctx context.Context, task string) error {
	systemPrompt, err := config.RenderSystem()
	if err != nil {
		return fmt.Errorf("render system prompt: %w", err)
	}
	userPrompt, err := config.RenderUserTask(task)
	if err != nil {
		return fmt.Errorf("render user prompt: %w", err)
	}
	a.messages = []types.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	step := 0
	roundsSinceTodo := 0 // 连续多少轮没调用 todo_write，用于注入 nag reminder
	for {
		if a.StepLimit > 0 && step >= a.StepLimit {
			return fmt.Errorf("reached step limit (%d)", a.StepLimit)
		}
		step++

		// Nag reminder：连续 3 轮没调 todo_write 就追加一条提醒（教学版机制）。
		if roundsSinceTodo >= 3 {
			a.messages = append(a.messages, types.Message{
				Role:    "user",
				Content: "<reminder>Update your todos.</reminder>",
			})
			roundsSinceTodo = 0
		}

		printStepHeader(step, a.StepLimit)

		// ① 问模型
		assistant, err := a.Model.Query(ctx, a.messages)
		if err != nil {
			return fmt.Errorf("model query failed: %w", err)
		}
		a.messages = append(a.messages, assistant)
		printAssistant(assistant)

		actions, _ := assistant.Extra["actions"].([]types.Action)
		if len(actions) == 0 {
			// 模型没有再发命令：若给出了文字回答，视为任务完成，正常结束。
			if strings.TrimSpace(assistant.Content) != "" {
				printDone()
				return nil
			}
			return errors.New("model returned empty content and no actions; aborting")
		}

		// 本轮是否调用了 todo_write
		calledTodo := false

		// ② 执行命令
		for _, act := range actions {
			if act.Tool == "todo_write" {
				calledTodo = true
				a.todos = act.Todos
				printTodos(a.todos)
				a.messages = append(a.messages, makeTodoResult(act))
				continue
			}

			if a.Confirm && !confirmCommand(act.Command) {
				a.messages = append(a.messages, makeToolResult(act, types.ExecResult{
					Output:     "[user rejected this command]",
					ReturnCode: -1,
					Exception:  "user_rejected",
				}))
				continue
			}

			printCommand(act.Command)
			result := a.Env.Execute(ctx, act)
			printResult(result)

			a.messages = append(a.messages, makeToolResult(act, result))

			// ③ 检查是否完成
			if environment.IsFinish(result) {
				printDone()
				return nil
			}
		}

		if calledTodo {
			roundsSinceTodo = 0
		} else {
			roundsSinceTodo++
		}
	}
}

// makeTodoResult 把 todo_write 调用包装成 tool result 消息，回执当前清单。
func makeTodoResult(act types.Action) types.Message {
	var sb strings.Builder
	sb.WriteString("Todos updated:\n")
	for _, t := range act.Todos {
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", t.Status, t.Content))
	}
	content := strings.TrimRight(sb.String(), "\n")
	if act.ToolCallID != "" {
		return types.Message{Role: "tool", ToolCallID: act.ToolCallID, Content: content}
	}
	return types.Message{Role: "user", Content: content}
}

// makeToolResult 把命令执行结果包装成 OpenAI tool/user 消息。
func makeToolResult(act types.Action, r types.ExecResult) types.Message {
	var sb strings.Builder
	if r.Exception != "" {
		_, _ = fmt.Fprintf(&sb, "<exception>%s</exception>\n", r.Exception)
	}
	_, _ = fmt.Fprintf(&sb, "<returncode>%d</returncode>\n", r.ReturnCode)
	out := r.Output
	if len(out) > 10000 { // 过长输出截断头尾，保留首尾各 5000 字
		out = out[:5000] + "\n...[elided]...\n" + out[len(out)-5000:]
	}
	_, _ = fmt.Fprintf(&sb, "<output>\n%s\n</output>", out)

	if act.ToolCallID != "" {
		return types.Message{
			Role:       "tool",
			ToolCallID: act.ToolCallID,
			Content:    sb.String(),
		}
	}
	// 兜底（模型不走 tool-calling 时）
	return types.Message{Role: "user", Content: sb.String()}
}

// ---------- 排版辅助 ----------

const sepWidth = 64

func printStepHeader(step, limit int) {
	title := fmt.Sprintf(" Step %d ", step)
	if limit > 0 {
		title = fmt.Sprintf(" Step %d / %d ", step, limit)
	}
	bar := strings.Repeat("─", (sepWidth-len([]rune(title)))/2)
	_, _ = fmt.Printf("\n%s%s%s%s%s%s\n", cBold, cCyan, bar, title, bar, cReset)
}

func printAssistant(m types.Message) {
	if s := strings.TrimSpace(m.Content); s != "" {
		_, _ = fmt.Printf("%s🤖 assistant%s\n%s\n", cBold+cMag, cReset, indent(s, "   "))
	}
	for _, tc := range m.ToolCalls {
		// todo_write 的清单由 printTodos 单独渲染，这里不重复打印原始参数。
		if tc.Function.Name == "todo_write" {
			continue
		}
		cmd := prettyToolArgs(tc.Function.Arguments)
		_, _ = fmt.Printf("%s🛠  %s%s\n%s\n", cBold+cBlue, tc.Function.Name, cReset, indent(cmd, "   "))
	}
}

// prettyToolArgs 尝试把 {"command":"..."} 解析出 command 单行返回，否则原样回退。
func prettyToolArgs(args string) string {
	var v struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &v); err == nil && v.Command != "" {
		return v.Command
	}
	return args
}

func printCommand(cmd string) {
	_, _ = fmt.Printf("\n%s💻 $%s %s\n", cBold+cYellow, cReset, cmd)
}

// printTodos 以勾选清单的形式渲染当前任务列表。
func printTodos(todos []types.TodoItem) {
	_, _ = fmt.Printf("\n%s📋 todos%s\n", cBold+cBlue, cReset)
	for _, t := range todos {
		var mark, color string
		switch t.Status {
		case "completed":
			mark, color = "✔", cGreen
		case "in_progress":
			mark, color = "▶", cYellow
		default:
			mark, color = "○", cDim
		}
		_, _ = fmt.Printf("   %s%s%s %s\n", color, mark, cReset, t.Content)
	}
}

func printResult(r types.ExecResult) {
	if r.Exception != "" {
		_, _ = fmt.Printf("%s⚠️  %s%s\n", cRed, r.Exception, cReset)
	}
	exitColor := cGreen
	if r.ReturnCode != 0 {
		exitColor = cRed
	}
	out := strings.TrimRight(r.Output, "\n")
	lines := 0
	if out != "" {
		lines = strings.Count(out, "\n") + 1
	}
	_, _ = fmt.Printf("%s↩ exit=%d%s%s  (%d lines)%s\n", exitColor, r.ReturnCode, cReset, cDim, lines, cReset)
	if out != "" {
		_, _ = fmt.Println(indent(out, "│ "))
	}
}

func printDone() {
	bar := strings.Repeat("─", sepWidth)
	_, _ = fmt.Printf("\n%s%s%s\n", cGreen, bar, cReset)
	_, _ = fmt.Printf("%s✅ Agent finished task.%s\n", cBold+cGreen, cReset)
	_, _ = fmt.Printf("%s%s%s\n", cGreen, bar, cReset)
}

// indent 给文本每一行前面加上 prefix。
func indent(s, prefix string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func confirmCommand(cmd string) bool {
	_, _ = fmt.Printf("\n%s👉 About to run:%s\n   %s\n%sExecute? [Y/n] %s", cBold+cYellow, cReset, cmd, cDim, cReset)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "" || answer == "y" || answer == "yes"
}
