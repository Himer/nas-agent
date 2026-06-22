// 本文件提供一个**极简 YAML 解析器**，零第三方依赖。
//
// 仅支持以下子集，足够 mini-agent 的配置文件使用：
//   - 顶层扁平 key: value
//   - 二级嵌套（一层缩进）：
//     model:
//     name: gpt-4o-mini
//     base_url: https://...
//   - # 行内注释
//   - 字符串可选用 "..." 或 '...' 包裹
//
// 不支持：列表、多层嵌套、多行字符串、锚点引用。
// 配置场景下这些都用不到，复杂需求用户可以走环境变量或命令行参数。
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// FileConfig 是从 config.yaml 解析出的配置。
//
// 所有字段都是可选；未配置的字段保持零值，由调用方决定如何回退到环境变量或默认值。
type FileConfig struct {
	// Model 相关
	ModelName string // model.name 或顶层 model_name
	BaseURL   string // model.base_url 或顶层 base_url
	APIKey    string // model.api_key 或顶层 api_key

	// Agent 相关
	StepLimit   int  // agent.step_limit 或顶层 step_limit
	SkipConfirm bool // agent.skip_confirm 或顶层 skip_confirm（true = 不询问直接执行命令）

	// Environment 相关
	Cwd     string // environment.cwd 或顶层 cwd
	Timeout int    // environment.timeout 或顶层 timeout（秒）

	// 默认任务（可选；命令行 -t 优先级更高）
	Task string // task
}

// LoadFile 从指定路径加载配置文件。文件不存在时返回空配置且不报错（视为"未配置"）。
func LoadFile(path string) (FileConfig, error) {
	var cfg FileConfig
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	defer func() { _ = f.Close() }()

	flat, err := parseSimpleYAML(f)
	if err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}

	cfg.ModelName = pick(flat, "model.name", "model_name")
	cfg.BaseURL = pick(flat, "model.base_url", "base_url")
	cfg.APIKey = pick(flat, "model.api_key", "api_key")
	cfg.Cwd = pick(flat, "environment.cwd", "cwd")
	cfg.Task = pick(flat, "task")

	if v := pick(flat, "agent.step_limit", "step_limit"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
			return cfg, fmt.Errorf("step_limit not an int: %q", v)
		}
		cfg.StepLimit = n
	}
	if v := pick(flat, "environment.timeout", "timeout"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
			return cfg, fmt.Errorf("timeout not an int: %q", v)
		}
		cfg.Timeout = n
	}
	if v := pick(flat, "agent.skip_confirm", "skip_confirm"); v != "" {
		cfg.SkipConfirm = v == "true" || v == "yes" || v == "1"
	}

	return cfg, nil
}

// pick 按顺序返回第一个非空 key。
func pick(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

// parseSimpleYAML 把 YAML 流解析成扁平 map，嵌套 key 用 "." 拼接。
//
// 例如：
//
//	model:
//	  name: gpt-4o-mini
//	step_limit: 50
//
// 解析为：
//
//	map["model.name"] = "gpt-4o-mini"
//	map["step_limit"] = "50"
func parseSimpleYAML(r *os.File) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var currentParent string // 当前所在的一级父键（缩进时使用）
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}

		indent := countLeadingSpaces(line)
		stripped := line[indent:]

		colon := strings.Index(stripped, ":")
		if colon < 0 {
			return nil, fmt.Errorf("line %d: missing ':' in %q", lineNo, raw)
		}
		key := strings.TrimSpace(stripped[:colon])
		value := strings.TrimSpace(stripped[colon+1:])
		value = unquote(value)

		switch indent {
		case 0:
			if value == "" {
				// 形如 "model:"，进入分组
				currentParent = key
			} else {
				out[key] = value
				currentParent = ""
			}
		default:
			// 任意正缩进都视为子项（不强制具体空格数，更宽容）
			if currentParent == "" {
				return nil, fmt.Errorf("line %d: unexpected indent without parent: %q", lineNo, raw)
			}
			out[currentParent+"."+key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func stripComment(line string) string {
	// 简单处理：# 不在引号中视为注释起点。
	inSingle, inDouble := false, false
	for i, c := range line {
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return line[:i]
			}
		}
	}
	return line
}

func countLeadingSpaces(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
