// Command mini 是 mini-agent 的 CLI 入口。
//
// 配置来源：
//   - model.*：仅从环境变量读取，不支持 CLI / 配置文件
//       MINI_AGENT_MODEL_NAME   模型名称（必填）
//       MINI_AGENT_BASE_URL     OpenAI 兼容 BaseURL（必填）
//       MINI_AGENT_API_KEY      API Key（必填）
//   - agent.* / environment.*：环境变量 + 命令行，命令行优先，不再支持配置文件
//       MINI_AGENT_STEP_LIMIT     / --step-limit
//       MINI_AGENT_SKIP_CONFIRM   / --skip-confirm
//       MINI_AGENT_CWD            / --cwd
//       MINI_AGENT_TIMEOUT        / --timeout
//   - 任务通过 --task 传入（必填）
//
// 用法：
//
//	mini --task "你的任务"
//	mini --task "..." --step-limit 50 --timeout 60 --skip-confirm
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Himer/mini-agent/pkg/agent"
	"github.com/Himer/mini-agent/pkg/environment"
	"github.com/Himer/mini-agent/pkg/model"
)

// 环境变量名集中定义，避免散落各处。
const (
	envModelName   = "MINI_AGENT_MODEL_NAME"
	envBaseURL     = "MINI_AGENT_BASE_URL"
	envAPIKey      = "MINI_AGENT_API_KEY"
	envStepLimit   = "MINI_AGENT_STEP_LIMIT"
	envSkipConfirm = "MINI_AGENT_SKIP_CONFIRM"
	envCwd         = "MINI_AGENT_CWD"
	envTimeout     = "MINI_AGENT_TIMEOUT"
)

func main() {
	// CLI 标志：默认值用 sentinel（int 用 -1）区分"用户未传"和"用户显式传零值"。
	// 仅 --task 提供短别名 -t，其他参数只保留长形式。
	var (
		task            string
		stepLimitFlag   int
		timeoutFlag     int
		cwdFlag         string
		skipConfirmFlag bool
	)
	flag.StringVar(&task, "task", "", "Task description (required)")
	flag.StringVar(&task, "t", "", "Short alias for --task")

	flag.IntVar(&stepLimitFlag, "step-limit", -1, "Max agent steps (overrides "+envStepLimit+")")
	flag.IntVar(&timeoutFlag, "timeout", -1, "Per-command timeout in seconds (overrides "+envTimeout+")")
	flag.StringVar(&cwdFlag, "cwd", "", "Working directory for commands (overrides "+envCwd+")")
	flag.BoolVar(&skipConfirmFlag, "skip-confirm", false, "Skip per-command confirmation prompt (overrides "+envSkipConfirm+")")

	flag.Parse()

	// 检测 --skip-confirm 是否被显式传入（用于区分默认值 false 与用户传 false）。
	skipConfirmSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "skip-confirm" {
			skipConfirmSet = true
		}
	})

	if strings.TrimSpace(task) == "" {
		_, _ = fmt.Fprintln(os.Stderr, "error: --task is required, e.g. --task \"看看本地哪个文件最大\"")
		flag.Usage()
		os.Exit(2)
	}

	// ----- model 配置：仅环境变量 -----
	modelName := strings.TrimSpace(os.Getenv(envModelName))
	baseURL := strings.TrimSpace(os.Getenv(envBaseURL))
	apiKey := strings.TrimSpace(os.Getenv(envAPIKey))
	var missing []string
	if modelName == "" {
		missing = append(missing, envModelName)
	}
	if baseURL == "" {
		missing = append(missing, envBaseURL)
	}
	if apiKey == "" {
		missing = append(missing, envAPIKey)
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintln(os.Stderr, "error: the following environment variables are required:")
		for _, k := range missing {
			_, _ = fmt.Fprintf(os.Stderr, "  - %s\n", k)
		}
		os.Exit(2)
	}

	// ----- agent / environment 配置：CLI 优先于环境变量 -----
	stepLimit, err := resolveInt(stepLimitFlag, envStepLimit, 30)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	if stepLimit <= 0 {
		_, _ = fmt.Fprintln(os.Stderr, "error: step-limit must be > 0")
		os.Exit(2)
	}

	timeout, err := resolveInt(timeoutFlag, envTimeout, 30)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	if timeout <= 0 {
		_, _ = fmt.Fprintln(os.Stderr, "error: timeout must be > 0")
		os.Exit(2)
	}

	cwd := cwdFlag
	if cwd == "" {
		cwd = os.Getenv(envCwd)
	}

	skipConfirm := false
	if skipConfirmSet {
		skipConfirm = skipConfirmFlag
	} else if v := os.Getenv(envSkipConfirm); v != "" {
		skipConfirm = parseBool(v)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	m := model.NewOpenAI(apiKey, baseURL, modelName)
	env := environment.NewLocal(cwd, time.Duration(timeout)*time.Second)
	a := agent.New(m, env, stepLimit, !skipConfirm)

	_, _ = fmt.Println("\x1b[1m\x1b[36m╭──────────────────────────────────────────────────────────────╮\x1b[0m")
	_, _ = fmt.Printf("\x1b[1m\x1b[36m│\x1b[0m \x1b[1m👋 mini-agent (go)\x1b[0m  \x1b[2mmodel=\x1b[0m%-32s         \x1b[1m\x1b[36m│\x1b[0m\n", truncate(modelName, 32))
	_, _ = fmt.Printf("\x1b[1m\x1b[36m│\x1b[0m \x1b[2mbase  :\x1b[0m %-52s \x1b[1m\x1b[36m│\x1b[0m\n", truncate(baseURL, 52))
	_, _ = fmt.Printf("\x1b[1m\x1b[36m│\x1b[0m \x1b[2mskip_confirm:\x1b[0m %-46v \x1b[1m\x1b[36m│\x1b[0m\n", skipConfirm)
	_, _ = fmt.Println("\x1b[1m\x1b[36m╰──────────────────────────────────────────────────────────────╯\x1b[0m")
	// task 长度不定，单独成块输出，避免和固定宽度的盒子打架。
	_, _ = fmt.Printf("\x1b[1m\x1b[36m📝 task:\x1b[0m\n%s\n", indentBlock(task, "   "))

	if err := a.Run(ctx, task); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "\n\x1b[1m\x1b[31m❌ %v\x1b[0m\n", err)
		os.Exit(1)
	}
}

// resolveInt 决策顺序：CLI（>=0 视为用户提供） > 环境变量 > 默认值。
func resolveInt(flagVal int, envKey string, def int) (int, error) {
	if flagVal >= 0 {
		return flagVal, nil
	}
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s is not a valid integer: %q", envKey, v)
		}
		return n, nil
	}
	return def, nil
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "on":
		return true
	}
	return false
}

// truncate 把字符串截断到 n 个 rune 以内，超出用省略号代替。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// indentBlock 把多行字符串每一行前面加上 prefix，便于成块显示。
func indentBlock(s, prefix string) string {
	if s == "" {
		return prefix
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
