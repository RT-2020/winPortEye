# 更新日志

本项目的所有重要变更都会记录在此文件中。

格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [0.3.2] - 2026-08-17

### 修复

- **「复制配置」按钮粘贴出旧内容**：原实现用 walk 库的剪贴板 API，其 `OpenClipboard` 后不 `EmptyClipboard`，导致旧格式（ANSI）数据残留，部分程序粘贴到的是上一次复制的内容。改为直接调 Win32 API 原子写入，同时写入 UTF-16 与 ANSI 两种格式

### 新增

- **更新检查节流**：24 小时内只做一次真实远端检查（状态存 `%APPDATA%\PortEye\updater.json`）；发现新版本不节流，网络失败不记录（避免把故障当「已确认无更新」）
- **下载可靠性**：断点续传（Range 头）；SHA-256 完整性校验（GitHub API 提供摘要）；损坏的半成品自动清除，不被续传机制复用
- **更新物料迁移**：下载/解压全部落到 `%TEMP%\PortEye` 工作目录，程序目录只读也能更新；清理本机数据同步新增该目录的清理
- **端口排除范围检测支持 IPv6**：IPv6 尽力查询（失败静默跳过），命中时按「IPv4/IPv6 + TCP/UDP」逐段列出提示
- **批量终止分级提示**：explorer 等终止后影响桌面的进程从红字警告改为黄字提示级，关键系统进程仍红字
- **提权杀进程确认**：UAC 同意后轮询确认目标进程已退出（约 8 秒），超时提示「可能受保护」
- **命令行输出**：`--version` / `--help` 挂到父进程控制台输出（发行 exe 无自带控制台）
- **关于页显示真实版本号**：版本号经 ldflags 注入并透传至设置面板

### 其他

- 端口排除检测、更新链路、清理逻辑补单元测试

## [0.3.1] - 2026-08-12

### 新增

- **PortEye Logo**：深蓝渐变圆角底 + 虹膜环形端口（6 青 2 暗，暗色代表空闲口）+ 瞳孔网络节点高光；exe 文件图标、任务栏、窗口、系统托盘全部启用
- exe 嵌入版本信息（`ProductVersion`、`FileDescription "PortEye"` 等），文件属性中可见

### 其他

- 托盘图标从 shell32 系统默认图标改为项目图标（加载失败自动回退系统图标）
- 图标资源经 go-winres 嵌入，manifest 保持外置不变

## [0.3.0] - 2026-08-12

### 新增

- **自动检查更新**：启动时后台静默检查 GitHub Releases；发现新版本时工具栏出现「有新版本」按钮，hover 展示更新日志（tooltip），点击才开始下载（按钮实时显示进度）；下载完成弹窗选择「立即重启更新」（退出→自动替换→启动新版）或「稍后」（下次启动直接提示安装，不重复下载）。主进程退出后由临时 bat 完成替换，失败自动回退拉起旧版
- **主表右键菜单**：右键进程行可直接终止选中进程，复用原有终止流程（确认框、系统进程警告、批量汇总）；右键自动校正选中行，命中已选中的多行时保持多选批量终止，无需再点左下角按钮

### 修复

- 已是管理员时，仍拿不到名/路径的进程改显示「系统进程 / 受保护」——原「权限不足 / 需管理员权限」文案会误导：此类进程（System/csrss/lsass 等）再提权也拿不到信息

### 其他

- 版本号注入：main.go 新增 `version` 变量，构建时以 `-ldflags "-X main.version=x.y.z"` 注入（README 构建命令同步）
- 零新增第三方依赖

## [0.2.1] - 2026-08-12

### 性能优化

内存优化专项：进程信息采集全链路从 gopsutil 迁移至原生 Win32 API，彻底移除 gopsutil 依赖，降低内存占用与二进制体积。

- 进程名/路径查询改用原生 `QueryFullProcessImageNameW`，合并为单次查询，消除重复取数
- 命令行参数、进程创建时间改用原生 API 获取
- 端口枚举改用原生 `GetExtendedTcpTable` / `GetExtendedUdpTable`，scanner 解耦 gopsutil
- 终止进程改用原生 `TerminateProcess`，core 包彻底解耦 gopsutil
- go.mod 移除 gopsutil 及其间接依赖

### 修复

- 提权检测改用 advapi32.dll，伪句柄改用 `^uintptr(0)`，修复 64 位下永远判为未提权
- `SetConns` 增加内容相等判断，数据未变时跳过重绘，避免子表闪烁
- 移除 `debug.SetMemoryLimit`，避免频繁 GC 导致 UI 整片重绘闪烁
- 刷新前后保存/恢复主表滚动位置
- 修复关闭按钮拦截逻辑，提权重启路径可真正退出程序

## [0.2.0] - 2026-08-04

### 新增

- 端口列表按进程聚合：主表按 PID 聚合同一进程占用的多个端口（展示端口数与端口摘要），选中进程后子表列出其全部端口明细，组成 master-detail 双表视图
- 批量终止：主表支持 Ctrl/Shift 多选，一次终止多个进程；命中关键系统进程（csrss/winlogon/lsass 等）弹红字警告防误杀，逐个终止后汇总成功/失败
- 以管理员重启：工具栏一键提权拉起 UAC，重启后可获取此前因权限不足无法识别的进程名/路径
- 端口排除范围检测：解析 netsh 排除端口段（中英文兼容，30s 缓存 + 后台预热），搜索命中内核预留段时显示黄色提示条
- 无抖动刷新：后台轮询改为按 PID 增量 diff 更新，仅变动行做增删改，焦点/滚动/多选选中位原样保留
- 添加 MIT LICENSE

## [0.1.0] - 2026-08-03

初始版本：PortEye Windows 端口监控工具。

- 端口监控：列出所有 TCP/UDP 连接（含 IPv4/IPv6）及占用进程的 PID/名称/路径
- 一键终止占用进程
- MCP 接入：5 个工具（list_ports / find_port / get_process / kill_process / kill_by_port），AI 助手可直接查询/管理端口
- 托盘常驻：最小化到托盘、开机自启、后台轮询
- 单文件 exe，绿色免安装

[0.3.2]: https://github.com/RT-2020/winPortEye/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/RT-2020/winPortEye/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/RT-2020/winPortEye/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/RT-2020/winPortEye/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/RT-2020/winPortEye/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/RT-2020/winPortEye/releases/tag/v0.1.0
