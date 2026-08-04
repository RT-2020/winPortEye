# PortEye

> Windows 端口监控工具 · 支持终止占用进程 · 接入 MCP 让 AI 助手也能查询/管理端口

一眼看穿端口被谁占用，并能一键终止。单文件 exe，绿色免安装。支持 MCP（Model Context Protocol），接入 Claude Desktop / Cursor / ZCode 等 AI 客户端后，AI 可直接帮你查端口、杀进程。

## 功能

- 🔍 **端口监控**：列出所有 TCP/UDP 连接（含 IPv4/IPv6）、占用进程的 PID / 名称 / 路径
- 🧩 **按进程聚合**：主表按 PID 聚合（同一进程占用的多个端口合并为一行，展示端口数与端口摘要），选中进程后子表列出其占用的全部端口明细
- 🛑 **批量终止**：主表支持多选（Ctrl/Shift），一次终止多个进程。用户态进程零 UAC，系统进程弹一次 UAC 提权。命中关键系统进程（csrss/winlogon/lsass 等）时弹红字警告防止误杀
- 👑 **管理员权限**：一键以管理员身份重启（拉起 UAC），重启后可获取此前因权限不足无法识别的进程名/路径
- 🤖 **MCP 接入**：5 个工具（list_ports / find_port / get_process / kill_process / kill_by_port），AI 助手可调用
- 📌 **托盘常驻**：最小化到托盘、开机自启、后台轮询
- 🎯 **无抖动刷新**：后台 3 秒轮询采用按 PID 增量 diff 更新（类似 Vue :key），仅变动行做增删改，焦点 / 滚动 / 多选选中位原样保留，操作中不被打断
- ⚙ **设置面板**：MCP 配置一键复制、MCP 自检、开机自启
- 🪶 **轻量**：单文件 ~11MB，静态编译无运行时依赖，内存占用低

## 构建

需要 Go 1.25+（依赖官方 MCP Go SDK）。

```bash
go build -ldflags="-s -w -H=windowsgui" -o porteye.exe .
```

产物为单文件 `porteye.exe`（约 10 MB）。`porteye.exe.manifest` 需与 exe 放同一目录（启用 Common Controls v6 + DPI 感知）。

## 使用

### 方式一：图形界面（双击 porteye.exe）

- **主表（进程聚合）**：一行一个进程，列为 PID / 进程 / 端口数 / 端口摘要 / 路径。同一进程占用的多个端口合并显示，符合「管理单位是进程」的实际操作习惯
- **子表（端口明细）**：选中主表某进程后，下方表格列出它占用的全部端口（协议 / 本地地址 / 端口 / 远端地址 / 状态）
- 搜索框实时过滤主表（PID / 端口摘要 / 进程名 / 路径），后台 3 秒轮询增量刷新（仅变动行做增删改，焦点 / 滚动 / 选中位不丢）
- 点表头排序；主表支持 Ctrl/Shift 多选，点「终止选中进程（N 个）」批量结束，命中关键系统进程会弹红字警告
- 进程名/路径显示为「权限不足」时，点工具栏「以管理员重启」提权后即可获取（UAC 确认）
- 关闭按钮 → 最小化到托盘（不退出）；托盘右键 → 显示窗口 / 开机自启 / 退出
- 点「⚙ 设置」打开设置面板：MCP 配置 / MCP 状态自检 / 通用 / 关于

### 方式二：MCP 接入 AI 助手

在 Claude Desktop / Cursor / ZCode 的 MCP 配置中加入（设置面板的「MCP 配置」Tab 可一键复制并含真实路径）：

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

接入后，AI 可调用 5 个工具：

| 工具 | 参数 | 说明 |
|---|---|---|
| `list_ports` | protocol?, state?, port? | 列出端口连接及占用进程 |
| `find_port` | port(必填), protocol? | 查指定端口被谁占用 |
| `get_process` | pid(必填) | 查进程详情（名/路径/命令行/创建时间） |
| `kill_process` | pid(必填) | 终止进程（用户态直接杀，系统态弹 UAC） |
| `kill_by_port` | port(必填), protocol? | 按端口杀（先查后杀） |

**示例对话**：
> "帮我查 8080 端口被哪个程序占了" → AI 调用 `find_port`
> "把占用 3000 的进程杀掉" → AI 调用 `kill_by_port`

## 权限说明

- **查询端口/进程**：普通用户权限即可
- **杀用户态进程**（自己启动的 node/python/IDE 等）：直接杀，无需提权
- **杀系统进程**（svchost/IIS/PID 4 System 等）：弹 UAC，需用户在桌面会话点击同意
- **MCP 模式下杀系统进程**：AI 客户端 spawn 的进程通常无交互桌面，UAC 会失败 —— 返回"需在桌面会话手动处理"提示
- **PPL 受保护进程**：即使提权也无法终止

## 环境要求与已知限制

### 系统要求
- Windows 7 及以上（依赖 IP Helper API 与 netsh）
- 端口枚举、进程查询、终止进程为核心功能，开箱即用，无外部依赖

### 端口排除范围检测（依赖 netsh）
搜索框输入纯数字端口时，PortEye 会检测该端口是否被 Windows 内核**预留**（区别于"进程占用"）：
- **何为预留**：Hyper-V / WSL2 / Docker Desktop 的 WinNAT 会动态预留端口段，这些端口**没有进程占用**（netstat/PortEye 主表查不到），但应用 `bind()` 会被内核拒绝
- **依赖 netsh**：检测通过调用 `netsh interface ipv4 show excludedportrange` 实现。**这是查询动态预留的唯一可行方式**——Windows 没有公开 API，注册表只覆盖手动配置的预留、覆盖不到动态预留
- **降级行为**：若 netsh 不可用（被组策略禁用 / 精简版系统裁掉 / 超时未响应），该功能静默降级——搜索端口且无进程占用时，提示"可能被内核预留但无法检测"，不影响其他功能

### 已知限制
- **非 PID 排序时刷新**：主表默认按进程名排序（系统进程垫底），刷新稳定无抖动。若手动改按端口数/路径等列排序，后台 3 秒刷新可能导致选中位轻微偏移（虚拟表格特性，可接受）
- **多选批量杀的系统进程保护**：批量终止时对 csrss/winlogon/lsass 等关键系统进程弹红字警告，但仍需用户自行判断，误杀可能导致系统不稳定

## 技术栈

- Go 1.25
- [gopsutil/v4](https://github.com/shirou/gopsutil) — 端口枚举 + 进程查询
- [官方 MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) — MCP server
- [lxn/walk](https://github.com/lxn/walk) — Windows 原生 GUI

## 目录结构

```
win/
├── main.go                      入口：--mcp 分流 / 默认 GUI
├── porteye.exe.manifest         Common Controls v6 + DPI + asInvoker
├── internal/
│   ├── core/                    核心能力（GUI 与 MCP 共用）
│   │   ├── types.go             数据结构
│   │   ├── scanner.go           端口枚举 + 过滤
│   │   ├── process.go           进程查询
│   │   ├── killer.go            两级杀进程（直接杀 / runas 提权）
│   │   ├── elevate.go           提权检测 + 以管理员重启
│   │   ├── excludedports.go     端口排除范围检测（netsh + 缓存 + 降级）
│   │   └── mcp_check.go         MCP 自检
│   ├── mcpserver/
│   │   └── server.go            MCP stdio server + 5 个工具
│   └── ui/
│       ├── model.go             子表数据模型（端口明细 + 排序）
│       ├── groupmodel.go        主表数据模型（按 PID 聚合 + 过滤+排序）
│       ├── window.go            主窗口（双表 master-detail）
│       ├── watcher.go           后台轮询
│       ├── tray.go              托盘 + 开机自启
│       └── settings.go          设置对话框（MCP配置/状态/通用/关于）
└── README.md
```

## License

MIT
