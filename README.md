# PortEye

Windows 端口监控工具：一眼看穿端口被谁占用，一键终止占用进程。单文件 exe，绿色免安装。支持 MCP，接入 Claude Desktop / Cursor / ZCode 等 AI 客户端后，AI 可直接帮你查端口、杀进程。

## 功能

- **端口监控**：列出所有 TCP/UDP 连接（含 IPv4/IPv6）及占用进程的 PID / 名称 / 路径
- **按进程聚合**：同一进程的多个端口合并为一行，选中后子表列出全部端口明细
- **批量终止**：主表多选（Ctrl/Shift）一次终止多个进程；用户态进程直接杀，系统进程弹一次 UAC 提权；命中关键系统进程（csrss/winlogon/lsass 等）弹红字警告，杀 explorer 弹黄字提示（会丢任务栏）
- **管理员重启**：一键以管理员身份重启，重启后可识别此前权限不足的进程名/路径
- **MCP 接入**：7 个工具（list_ports / find_port / get_process / kill_process / kill_by_port / export_ports / process_tree）
- **托盘常驻**：最小化到托盘、开机自启、后台 3 秒轮询刷新（增量更新，不打断操作）
- **自动检查更新**：启动时静默检查 GitHub Releases，有新版本一键下载、重启即完成更新；支持断点续传与 SHA-256 校验，下载物放 `%TEMP%\PortEye`（程序目录只读也能更新）；检查有 24 小时节流
- **设置面板**：MCP 配置一键复制、MCP 自检、开机自启、清理本机数据、关于页显示真实版本号
- **日志轮转**：app.log 超过 1MB 自动轮转为 app.log.old
- **轻量**：单文件约 12MB，无运行时依赖，常驻内存约 20MB

## 构建

需要 Go 1.25+。

```bash
go build -ldflags="-s -w -H=windowsgui -X main.version=0.3.2" -o porteye.exe .
```

`-X main.version=0.3.2` 注入版本号（用于检查更新的版本比较，按实际发版号替换；不注入时为 `dev`，不触发更新）。`porteye.exe.manifest` 需与 exe 同目录（Common Controls v6 + DPI 感知）。

## 使用

### 图形界面（双击 porteye.exe）

- **主表**：一行一个进程（PID / 进程 / 端口数 / 端口摘要 / 路径），搜索框实时过滤，点表头排序
- **子表**：选中主表进程后显示其全部端口明细（协议 / 本地地址 / 端口 / 远端地址 / 状态）
- 右键进程行可直接终止；关闭按钮最小化到托盘（不退出）；托盘右键可显示窗口 / 开机自启 / 退出
- 进程名显示「系统进程 / 受保护」时无法获取信息，即使提权也不行（System/csrss 等受保护进程）
- 点「⚙ 设置」打开设置面板：MCP 配置 / MCP 状态自检 / 通用 / 关于

### MCP 接入 AI 助手

在 Claude Desktop / Cursor / ZCode 的 MCP 配置中加入（设置面板「MCP 配置」Tab 可一键复制，含真实路径）：

```json
{
  "mcpServers": {
    "porteye": {
      "command": "C:\\path\\to\\porteye.exe",
      "args": ["--mcp"]
    }
  }
}
```

接入后 AI 可调用 7 个工具：

| 工具 | 参数 | 说明 |
|---|---|---|
| `list_ports` | protocol?, state?, port? | 列出端口连接及占用进程 |
| `find_port` | port(必填), protocol? | 查指定端口被谁占用 |
| `get_process` | pid(必填) | 查进程详情（名/路径/命令行/创建时间） |
| `kill_process` | pid(必填) | 终止进程（用户态直接杀，系统态弹 UAC） |
| `kill_by_port` | port(必填), protocol? | 按端口杀（先查后杀） |
| `export_ports` | protocol?, state? | 导出端口连接为 CSV 文本（含表头），便于存档/分析 |
| `process_tree` | （无参数） | 枚举全部进程及父 PID，可构建进程树、评估杀进程连带影响 |

## 权限说明

- **查询端口/进程**：普通权限即可
- **杀用户态进程**（自己启动的 node/python/IDE 等）：直接杀，无需提权
- **杀系统进程**（svchost/IIS/PID 4 System 等）：弹 UAC，需桌面会话确认；UAC 同意后程序会确认目标已退出（约 8 秒），超时提示「可能受保护」
- **MCP 模式杀系统进程**：AI 客户端 spawn 的进程通常无交互桌面，UAC 会失败，返回「需在桌面会话手动处理」
- **PPL 受保护进程**：提权也无法终止

## 已知限制

- **端口排除范围检测**（搜索框输入纯数字端口时触发）：Hyper-V / WSL2 / Docker 的 WinNAT 会动态预留端口段，这些端口**没有进程占用**，但应用 `bind()` 会被内核拒绝。检测依赖 `netsh`（Windows 没有公开 API），netsh 不可用时静默降级，提示「可能被内核预留但无法检测」。IPv4 全量检测，IPv6 尽力检测
- **更新检查节流**：24 小时内只检查一次远端，新版本发布后最多延迟 24 小时被发现（点「检查更新」不受此限）
- **命令行输出**：`--version` / `--help` 需在 cmd/PowerShell 里运行才可见（双击无控制台）

## 技术栈

- Go 1.25
- 原生 Win32 API（IP Helper / NT API）：端口枚举、进程查询、进程终止
- [官方 MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)：MCP server
- [lxn/walk](https://github.com/lxn/walk)：Windows 原生 GUI

## 目录结构

```
win/
├── main.go                      入口：--mcp 分流 / --version / --help / 默认 GUI
├── porteye.exe.manifest         Common Controls v6 + DPI + asInvoker
├── internal/
│   ├── core/                    核心能力（GUI 与 MCP 共用）
│   │   ├── scanner.go           端口枚举 + 过滤
│   │   ├── process.go           进程查询
│   │   ├── killer.go            两级杀进程（直接杀 / runas 提权）
│   │   ├── elevate.go           提权检测 + 以管理员重启
│   │   ├── excludedports.go     端口排除范围检测（netsh + 缓存 + 降级）
│   │   ├── mcp_check.go         MCP 自检
│   │   ├── updater.go           更新下载 / 校验 / 解压 / 重启替换
│   │   ├── updatecheck.go       远端检查节流（24h）
│   │   └── cleanup.go           清理本机数据（日志/自启/更新残留/TEMP）
│   ├── mcpserver/
│   │   └── server.go            MCP stdio server + 7 个工具
│   └── ui/
│       ├── window.go            主窗口（双表 master-detail）
│       ├── groupmodel.go        主表数据模型（按 PID 聚合）
│       ├── model.go             子表数据模型
│       ├── watcher.go           后台轮询 + 日志轮转
│       ├── tray.go              托盘 + 开机自启
│       ├── settings.go          设置对话框（MCP配置/状态/通用/关于）
│       ├── clipboard.go         剪贴板写入（Win32 API）
│       └── updater.go           更新按钮 UI 控制
└── README.md
```

## License

MIT
