// Package core 的 processtree.go：全进程枚举（Toolhelp32 快照）。
// 供 MCP process_tree 工具使用：返回所有进程的 PID/父 PID/名称，
// 路径尽力查询（受保护进程可能为空），CommandLine/CreateTime 不填
// （全量枚举下开销过大，保持 0/空）。
package core

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Toolhelp32 快照 API（kernel32.dll）。
var (
	procCreateToolhelp32Snapshot = modkernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = modkernel32.NewProc("Process32FirstW")
	procProcess32NextW           = modkernel32.NewProc("Process32NextW")
)

// TH32CS_SNAPPROCESS：枚举系统全部进程。
const th32csSnapshotProcess = 0x2

// processEntry32W 是 PROCESSENTRY32W（进程快照条目）。
// dwSize 必须在调用 Process32FirstW 前赋值为 sizeof（API 按此校验结构版本）。
type processEntry32W struct {
	dwSize              uint32
	cntUsage            uint32
	th32ProcessID       uint32
	th32DefaultHeapID   uintptr
	th32ModuleID        uint32
	cntThreads          uint32
	th32ParentProcessID uint32
	pcPriClassBase      int32
	dwFlags             uint32
	szExeFile           [260]uint16 // MAX_PATH
}

// ListProcesses 枚举系统全部进程（Toolhelp32 快照）。
// 用于 MCP process_tree：返回所有进程的 PID/父 PID/名称，路径尽力查询
// （受保护进程可能为空）。CommandLine/CreateTime 字段不填（全量枚举下开销过大，
// 保持 0/空）。
func ListProcesses() ([]ProcessInfo, error) {
	snapshot, _, _ := procCreateToolhelp32Snapshot.Call(
		uintptr(th32csSnapshotProcess),
		uintptr(0), // 快照当前进程集，不指定进程
	)
	// 失败返回 INVALID_HANDLE_VALUE（-1），顺带兜底 0
	if snapshot == 0 || snapshot == uintptr(^uintptr(0)) {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot 失败")
	}
	defer procCloseHandle.Call(snapshot)

	var entry processEntry32W
	entry.dwSize = uint32(unsafe.Sizeof(entry))
	ret, _, _ := procProcess32FirstW.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		return nil, fmt.Errorf("Process32FirstW 失败：进程快照为空")
	}

	// 复用一次扫描的查询上下文（缓冲区 + PID 缓存），避免为每个进程重复分配
	q := newProcQueryContext()
	result := make([]ProcessInfo, 0, 256)
	for {
		info := ProcessInfo{
			Pid:       int32(entry.th32ProcessID),
			ParentPid: int32(entry.th32ParentProcessID),
			Name:      syscall.UTF16ToString(entry.szExeFile[:]),
		}
		if info.Pid > 0 {
			// 路径尽力查询：受保护进程 OpenProcess 失败，namePath 返回空串
			_, info.Path = q.namePath(info.Pid)
		}
		result = append(result, info)

		ret, _, _ = procProcess32NextW.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			break // 枚举完毕（含"中途失败"语义：到此结束，已收集的照常返回）
		}
	}
	return result, nil
}
