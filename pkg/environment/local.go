// Package environment 提供命令执行环境实现。
//
// 对应 Python 版 minisweagent/environments/local.py。
package environment

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Himer/mini-agent/pkg/types"
)

// FinishMarker 是 AI 用来标记任务完成的特殊字符串。
// 与 Python 版完全一致，便于复用 Python 版的提示词。
const FinishMarker = "COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT"

// Local 是本地命令执行环境。
//
// 重要设计：每次 Execute 都新开一个 shell 进程（subprocess 风格），
// 也就是说 cd / export 不会跨命令保持。这与 Python 版语义一致。
type Local struct {
	Cwd     string        // 命令工作目录，空则用进程当前目录
	Timeout time.Duration // 单条命令超时
}

// NewLocal 创建一个本地环境。timeout 为 0 时默认 30 秒。
func NewLocal(cwd string, timeout time.Duration) *Local {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Local{Cwd: cwd, Timeout: timeout}
}

// Execute 执行一条命令，使用系统默认 shell。
//
//   - Windows: 走 powershell -Command
//   - 其它系统: 走 sh -c
//
// 返回结果包含 stdout+stderr 合并输出和退出码。
func (l *Local) Execute(ctx context.Context, action types.Action) types.ExecResult {
	ctx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()

	cmd := buildShellCommand(ctx, action.Command)
	cmd.Dir = l.Cwd

	out, err := cmd.CombinedOutput()
	output := string(out)
	res := types.ExecResult{Output: output}

	if ctx.Err() == context.DeadlineExceeded {
		res.ReturnCode = -1
		res.Exception = fmt.Sprintf("command timed out after %s", l.Timeout)
		return res
	}
	if err != nil {
		// ExitError 是命令本身返回非零，仍然算正常执行；其它错误归为异常。
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ReturnCode = exitErr.ExitCode()
		} else {
			res.ReturnCode = -1
			res.Exception = err.Error()
		}
		return res
	}
	res.ReturnCode = 0
	return res
}

// IsFinish 检查命令输出是否包含完成标记，用于结束 Agent 循环。
//
// 与 Python 版一致：输出"第一行"必须正好等于 FinishMarker。
func IsFinish(r types.ExecResult) bool {
	if r.ReturnCode != 0 {
		return false
	}
	trimmed := strings.TrimLeft(r.Output, " \t\r\n")
	if trimmed == "" {
		return false
	}
	firstLine := trimmed
	if idx := strings.IndexAny(trimmed, "\r\n"); idx >= 0 {
		firstLine = trimmed[:idx]
	}
	return strings.TrimSpace(firstLine) == FinishMarker
}

func buildShellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}
