// Package core 的 winapi.go：封装 PortEye 用到的 Win32 系统调用。
//
// 设计原则：
//   - 集中管理 unsafe/syscall，避免散落在 scanner/process/killer 各文件；
//   - 用 LazyDLL + Find() 防御性检测 API 存在性（Vista+ 必然存在，检测仅作兜底）；
//   - 这层只做「薄封装」，不做业务逻辑，业务逻辑留在调用方。
//
// 替换的 gopsutil 调用：ExeWithContext / NameWithContext / Kill / Connections
// 都底层调用这里的 API，本文件去掉 gopsutil 的对象/context/错误包装开销。
package core

import (
	"fmt"
	"syscall"
	"unsafe"
)

// 进程查询/操作相关的 Win32 API。
var (
	modkernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess                   = modkernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW    = modkernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle                   = modkernel32.NewProc("CloseHandle")
	procGetCurrentProcessId           = modkernel32.NewProc("GetCurrentProcessId")
	procGetProcessTimes               = modkernel32.NewProc("GetProcessTimes")
	procTerminateProcess              = modkernel32.NewProc("TerminateProcess")
)

// OpenProcess 访问权限位（只定义用到的）。
const (
	processQueryInformation       = 0x0400 // PROCESS_QUERY_INFORMATION（Vista 之前的全权限）
	processQueryLimitedInformation = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION（Vista+，权限门槛更低）
)

// queryProcessImagePath 用 QueryFullProcessImageNameW 查询进程的完整 exe 路径。
// 用 PROCESS_QUERY_LIMITED_INFORMATION（Vista+），权限门槛低，能查到更多进程。
// 权限不足/系统进程/进程已退出 → 返回空串和 nil error（与 gopsutil 行为一致）。
//
// 实现参照 gopsutil v4.26.7 process_windows.go 的 ExeWithContext。
// buf 由调用方传入以便复用（避免每次 make）；至少需 MAX_LONG_PATH(520) 字符。
func queryProcessImagePath(pid int32, buf []uint16) (string, error) {
	if pid <= 0 {
		return "", nil
	}

	// 防御性检测：Vista+ 必然存在此 API，Find 失败说明运行在不支持的系统上
	if err := procQueryFullProcessImageNameW.Find(); err != nil {
		return "", fmt.Errorf("QueryFullProcessImageNameW 不可用: %w", err)
	}

	// OpenProcess 句柄，失败按"拿不到"处理（权限不足/进程已退出）
	handle, _, _ := procOpenProcess.Call(
		uintptr(processQueryLimitedInformation),
		uintptr(0), // bInheritHandle = FALSE
		uintptr(pid),
	)
	if handle == 0 {
		// 权限不足或进程不存在，返回空串（与 gopsutil 一致，不当作硬错误）
		return "", nil
	}
	defer procCloseHandle.Call(handle)

	// QueryFullProcessImageNameW(hProcess, dwFlags=0, lpExeName, lpdwSize)
	size := uint32(len(buf))
	ret, _, _ := procQueryFullProcessImageNameW.Call(
		handle,
		uintptr(0), // dwFlags=0 表示用 Win32 路径格式（非 \Device\... 原生格式）
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return "", nil
	}
	return syscall.UTF16ToString(buf[:size]), nil
}

// getCurrentPID 返回当前进程的 PID（供测试使用，避免在测试代码里引入更多依赖）。
func getCurrentPID() uint32 {
	ret, _, _ := procGetCurrentProcessId.Call()
	return uint32(ret)
}

// getProcessCreateTime 用 GetProcessTimes 取进程创建时间（毫秒，自 epoch）。
// 权限不足/进程不存在 → 返回 0（与 gopsutil createTimeWithContext 行为一致）。
//
// 实现参照 gopsutil v4.26.7 的 getRusage（底层就是 GetProcessTimes）。
// FILETIME 是 1601-01-01 起的 100ns 单位，需换算成 1970 epoch 毫秒。
func getProcessCreateTime(pid int32) int64 {
	if pid <= 0 {
		return 0
	}
	handle, _, _ := procOpenProcess.Call(
		uintptr(processQueryLimitedInformation),
		uintptr(0),
		uintptr(pid),
	)
	if handle == 0 {
		return 0
	}
	defer procCloseHandle.Call(handle)

	var creation, exit, kernel, user syscall.Filetime
	ret, _, _ := procGetProcessTimes.Call(
		handle,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if ret == 0 {
		return 0
	}
	// syscall.Filetime.Nanoseconds() 已封装了 1601→epoch 换算
	return creation.Nanoseconds() / 1e6
}

// processExists 判断进程是否存在（用 OpenProcess 成败判定）。
// 替代 gopsutil 的 PidExists（gopsutil 用 SYNCHRONIZE+WaitForSingleObject，语义略强，
// 但这里成否判定对 killer 的两级杀流程足够：误判存在时后续 TerminateProcess 失败会
// 自然落到第 2 级 runas taskkill，观察到的 KillResult.Message 基本一致）。
func processExists(pid int32) bool {
	if pid <= 0 {
		return false
	}
	handle, _, _ := procOpenProcess.Call(
		uintptr(processQueryLimitedInformation),
		uintptr(0),
		uintptr(pid),
	)
	if handle == 0 {
		return false
	}
	procCloseHandle.Call(handle)
	return true
}

// terminateProcess 终止指定进程。
// 返回 (true, nil) 表示成功；(false, err) 表示失败（权限不足/受保护/进程不存在）。
// 替代 gopsutil 的 process.Kill（底层就是 TerminateProcess）。
//
// exitCode 固定传 1（与 taskkill /F 的默认退出码一致）。
func terminateProcess(pid int32) error {
	if pid <= 0 {
		return fmt.Errorf("无效 PID: %d", pid)
	}
	// TerminateProcess 需要 PROCESS_TERMINATE (0x0001) 权限
	const processTerminate = 0x0001
	handle, _, _ := procOpenProcess.Call(
		uintptr(processTerminate),
		uintptr(0),
		uintptr(pid),
	)
	if handle == 0 {
		return fmt.Errorf("OpenProcess 失败（权限不足或进程不存在）")
	}
	defer procCloseHandle.Call(handle)

	ret, _, _ := procTerminateProcess.Call(handle, uintptr(1))
	if ret == 0 {
		return fmt.Errorf("TerminateProcess 失败（可能受保护）")
	}
	return nil
}
