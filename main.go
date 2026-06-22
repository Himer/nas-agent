// Command mini 是 mini-agent 的 CLI 入口。
//
// 设计原则：
//   - 所有"配置项"只从 YAML 文件读取（不读环境变量，无内置默认值）。
//   - 仅 task 通过命令行 --task 传入；YAML 中的 task 字段不再使用。
//
// 用法：
//
//	mini --task "你的任务"                          # 使用 ./config.yaml
//	mini --config my.yaml --task "..."              # 指定其它配置文件
//
// YAML 中需要提供（缺一不可）：
//
//	model.name
//	model.base_url
//	model.api_key
//	agent.step_limit
//	environment.timeout
//
// 可选字段：
//
//	agent.skip_confirm    (默认 false)
//	environment.cwd       (默认当前目录)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Himer/mini-agent/pkg/agent"
	"github.com/Himer/mini-agent/pkg/config"
	"github.com/Himer/mini-agent/pkg/environment"
	"github.com/Himer/mini-agent/pkg/model"
)

func main() {
	cfgFile := flag.String("config", "config.yaml", "Path to config.yaml (only source for model/agent/environment settings)")
	task := flag.String("task", "", "Task description (required; passed via CLI, not YAML)")
	flag.Parse()

	if strings.TrimSpace(*task) == "" {
		_, _ = fmt.Fprintln(os.Stderr, "error: --task is required, e.g. --task \"看看本地哪个文件最大\"")
		flag.Usage()
		os.Exit(2)
	}

	if _, err := os.Stat(*cfgFile); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: config file %s not found; YAML is required for all settings\n", *cfgFile)
		os.Exit(2)
	}
	cfg, err := config.LoadFile(*cfgFile)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: failed to load %s: %v\n", *cfgFile, err)
		os.Exit(2)
	}
	requireYAML(cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	m := model.NewOpenAI(cfg.APIKey, cfg.BaseURL, cfg.ModelName)
	env := environment.NewLocal(cfg.Cwd, time.Duration(cfg.Timeout)*time.Second)
	a := agent.New(m, env, cfg.StepLimit, !cfg.SkipConfirm)

	_, _ = fmt.Println("\x1b[1m\x1b[36m╭──────────────────────────────────────────────────────────────╮\x1b[0m")
	_, _ = fmt.Printf("\x1b[1m\x1b[36m│\x1b[0m \x1b[1m👋 mini-agent (go)\x1b[0m  \x1b[2mmodel=\x1b[0m%-32s         \x1b[1m\x1b[36m│\x1b[0m\n", truncate(cfg.ModelName, 32))
	_, _ = fmt.Printf("\x1b[1m\x1b[36m│\x1b[0m \x1b[2mbase  :\x1b[0m %-52s \x1b[1m\x1b[36m│\x1b[0m\n", truncate(cfg.BaseURL, 52))
	_, _ = fmt.Printf("\x1b[1m\x1b[36m│\x1b[0m \x1b[2mconfig:\x1b[0m %-52s \x1b[1m\x1b[36m│\x1b[0m\n", truncate(*cfgFile, 52))
	_, _ = fmt.Printf("\x1b[1m\x1b[36m│\x1b[0m \x1b[2mtask  :\x1b[0m %-52s \x1b[1m\x1b[36m│\x1b[0m\n", truncate(*task, 52))
	_, _ = fmt.Printf("\x1b[1m\x1b[36m│\x1b[0m \x1b[2mskip_confirm:\x1b[0m %-46v \x1b[1m\x1b[36m│\x1b[0m\n", cfg.SkipConfirm)
	_, _ = fmt.Println("\x1b[1m\x1b[36m╰──────────────────────────────────────────────────────────────╯\x1b[0m")

	if err := a.Run(ctx, *task); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "\n\x1b[1m\x1b[31m❌ %v\x1b[0m\n", err)
		os.Exit(1)
	}
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

// requireYAML 校验 YAML 中必须存在的字段；任何缺失都直接退出，不做兜底。
// 注意：task 由命令行 --task 提供，不在此处校验。
func requireYAML(cfg config.FileConfig) {
	var missing []string
	if strings.TrimSpace(cfg.ModelName) == "" {
		missing = append(missing, "model.name")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		missing = append(missing, "model.base_url")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		missing = append(missing, "model.api_key")
	}
	if cfg.StepLimit <= 0 {
		missing = append(missing, "agent.step_limit (must be > 0)")
	}
	if cfg.Timeout <= 0 {
		missing = append(missing, "environment.timeout (must be > 0)")
	}
	if len(missing) > 0 {
		_, _ = fmt.Fprintln(os.Stderr, "error: the following YAML fields are required:")
		for _, k := range missing {
			_, _ = fmt.Fprintf(os.Stderr, "  - %s\n", k)
		}
		os.Exit(2)
	}
}
