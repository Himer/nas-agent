# mini-agent (Go)

仓库地址：[github.com/Himer/mini-agent](https://github.com/Himer/mini-agent)

专为我的 NAS 准备的极简 AI Agent —— NAS 系统精简、装不上 Claude Code，于是用 Go 写一个零依赖版本，直接交叉编译成单个二进制丢上去，就能让它"自己跑命令"处理 NAS 上的日常任务。

---

## 使用示例

下图是在 NAS 上让它"把 `/data1/season1` 下的 mkv 文件批量重命名为纯数字文件名"的实际运行截图：

![mini-agent 运行示例](resource/example.png)

---

## 1. 准备配置

```bash
cp config.example.yaml config.yaml
```

编辑 `config.yaml`，填入你的模型信息：

```yaml
model:
  name: deepseek-chat
  base_url: https://api.deepseek.com/v1
  api_key: sk-xxxxxxxxxxxx

agent:
  step_limit: 30
  skip_confirm: false      # true = 不询问直接执行命令

environment:
  cwd: ""                  # 空 = 当前目录
  timeout: 30              # 单条命令超时（秒）
```

> 所有配置 **只从 YAML 读取**，命令行只负责传 `--task`。

> ⚠️ **强烈建议保持 `skip_confirm: false`，数据无价。**
> 开启 `skip_confirm: true` 后 AI 生成的每一条命令都会直接执行，没有人工确认环节。

## 2. 运行

```bash
# 使用默认 ./config.yaml
go run . --task "在当前目录创建一个 hello.py 并运行它打印 Hello World"

# 指定其它配置文件
go run . --config /path/to/my.yaml --task "..."
```

## 3. 编译为可执行文件

```bash
go build -o mini.exe .

# Windows
./mini.exe --task "看看本地哪个文件最大"

# Linux / macOS
./mini --task "看看本地哪个文件最大"
```

## 命令行参数

| 参数         | 说明           | 默认值             |
| ---------- | ------------ | --------------- |
| `--task`   | 任务描述（**必填**） | -               |
| `--config` | 配置文件路径       | `./config.yaml` |

## 自动发布

仓库已配置 GitHub Actions（`.github/workflows/release.yml`），打 tag 即可自动交叉编译并发布到 Releases：

```bash
git tag v0.1.0
git push origin v0.1.0
```

会同时产出以下平台的压缩包（每个包内含二进制 + `config.example.yaml` + `README.md`）：

- Linux：`amd64` / `arm64` / `armv7` / `armv6` / `386` / `mipsle` / `mips64le`（覆盖常见 NAS / 路由器 / 嵌入式）
- macOS：`amd64` / `arm64`
- Windows：`amd64` / `arm64`

由于纯 Go + 标准库，`CGO_ENABLED=0`，不需要任何系统依赖。

