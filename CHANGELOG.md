# 更新日志

本项目的所有重要变更都会记录在此文件中。

格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

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

初始版本：PortEye 端口之眼 Windows 端口监控工具。

- 端口监控：列出所有 TCP/UDP 连接（含 IPv4/IPv6）及占用进程的 PID/名称/路径
- 一键终止占用进程
- MCP 接入：5 个工具（list_ports / find_port / get_process / kill_process / kill_by_port），AI 助手可直接查询/管理端口
- 托盘常驻：最小化到托盘、开机自启、后台轮询
- 单文件 exe，绿色免安装

[0.2.1]: https://github.com/RT-2020/winPortEye/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/RT-2020/winPortEye/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/RT-2020/winPortEye/releases/tag/v0.1.0
