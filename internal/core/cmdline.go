// Package core 的 cmdline.go：通过 PEB 遍历读取进程命令行。
//
// 本文件实现移植自 gopsutil v4.24.8 process_windows.go 的 getProcessCommandLine
// （BSD-3-Clause License，版权归属 gopsutil 作者）。移植目的：摆脱 gopsutil 依赖，
// 把命令行查询（冷路径，仅 MCP get_process 工具使用）改为原生实现。
//
// 实现原理（64 位主程序读目标进程）：
//  1. OpenProcess(QUERY_INFORMATION | VM_READ) 拿句柄
//  2. 判断目标进程是不是 32 位（WOW64）：NtQueryInformationProcess(ProcessWow64Information)
//  3. 定位 PEB 地址：
//     - 32 位目标：NtQueryInformationProcess(ProcessWow64Information) 返回 32 位 PEB
//     - 64 位目标：NtQueryInformationProcess(ProcessBasicInformation) 取 PebBaseAddress
//  4. ReadProcessMemory 读 PEB → ProcessParameters → CommandLine（UTF-16 字符串）
//
// 对系统进程/受保护进程必然失败（权限不足），降级返回空串——与 gopsutil 现状一致。
//
// 仅实现「64 位主程序读 32/64 位目标」（GOARCH=amd64 的实际场景），
// 不实现「32 位主程序读 64 位目标」（需 NtWow64 系列函数，PortEye 不编译 32 位）。
package core

import (
	"syscall"
	"unsafe"
)

// ntdll 的原生 API（gopsutil 用 internal/common 包封装，这里直接声明）。
var (
	modnt                          = syscall.NewLazyDLL("ntdll.dll")
	procNtQueryInformationProcess  = modnt.NewProc("NtQueryInformationProcess")
	procNtReadVirtualMemory        = modnt.NewProc("NtReadVirtualMemory")
)

// NtQueryInformationProcess 的 InformationClass 常量。
const (
	processBasicInfo    = 0  // ProcessBasicInformation
	processWow64Info    = 26 // ProcessWow64Information
)

// PROCESS_BASIC_INFORMATION（64 位版）：NtQueryInformationProcess(ProcessBasicInformation) 的输出。
// 字段名对照 Windows 文档（64 位布局：Reserved1 + PebBaseAddress + Reserved2[2] +
// UniqueProcessId + InheritedFromUniqueProcessId；现有代码只用到 PebBaseAddress）。
type processBasicInformation64 struct {
	Reserved1                    uint64
	PebBaseAddress               uint64
	Reserved2                    [2]uint64
	UniqueProcessId              uint64
	InheritedFromUniqueProcessId uint64
}

// PEB（进程环境块），只取用到的字段。32 位版。
type processEnvironmentBlock32 struct {
	Reserved1         [2]uint8
	BeingDebugged     uint8
	Reserved2         uint8
	Reserved3         [2]uint32
	Ldr               uint32
	ProcessParameters uint32
}

// PEB 64 位版。注意 64 位下 ProcessParameters 前有对齐填充。
type processEnvironmentBlock64 struct {
	Reserved1         [2]uint8
	BeingDebugged     uint8
	Reserved2         uint8
	_                 [4]uint8 // 64 位对齐填充
	Reserved3         [2]uint64
	Ldr               uint64
	ProcessParameters uint64
}

// RTL_USER_PROCESS_PARAMETERS（32 位版），只取用到的字段。
type rtlUserProcessParameters32 struct {
	Reserved1                      [16]uint8
	ConsoleHandle                  uint32
	ConsoleFlags                   uint32
	StdInputHandle                 uint32
	StdOutputHandle                uint32
	StdErrorHandle                 uint32
	CurrentDirectoryPathNameLength uint16
	_                              uint16
	CurrentDirectoryPathAddress    uint32
	CurrentDirectoryHandle         uint32
	DllPathNameLength              uint16
	_                              uint16
	DllPathAddress                 uint32
	ImagePathNameLength            uint16
	_                              uint16
	ImagePathAddress               uint32
	CommandLineLength              uint16
	_                              uint16
	CommandLineAddress             uint32
	EnvironmentAddress             uint32
}

// RTL_USER_PROCESS_PARAMETERS（64 位版）。
type rtlUserProcessParameters64 struct {
	Reserved1                      [16]uint8
	ConsoleHandle                  uint64
	ConsoleFlags                   uint64
	StdInputHandle                 uint64
	StdOutputHandle                uint64
	StdErrorHandle                 uint64
	CurrentDirectoryPathNameLength uint16
	_                              uint16
	_                              uint32
	CurrentDirectoryPathAddress    uint64
	CurrentDirectoryHandle         uint64
	DllPathNameLength              uint16
	_                              uint16
	_                              uint32
	DllPathAddress                 uint64
	ImagePathNameLength            uint16
	_                              uint16
	_                              uint32
	ImagePathAddress               uint64
	CommandLineLength              uint16
	_                              uint16
	_                              uint32
	CommandLineAddress             uint64
	EnvironmentAddress             uint64
}

// getProcessCommandLine 读取指定进程的命令行（UTF-16 字符串）。
// 权限不足/系统进程/目标已退出 → 返回空串和 nil（与 gopsutil 行为一致）。
//
// 注：本函数假定主程序是 64 位（GOARCH=amd64），覆盖读 32 位和 64 位目标进程。
func getProcessCommandLine(pid int32) string {
	if pid <= 0 {
		return ""
	}

	// OpenProcess 需要 QUERY_INFORMATION + VM_READ（比查 Name/Path 多要 VM_READ）
	const processVMRead = 0x0010
	handle, _, _ := procOpenProcess.Call(
		uintptr(processQueryInformation|processVMRead),
		uintptr(0),
		uintptr(pid),
	)
	if handle == 0 {
		// 权限不足或进程不存在
		return ""
	}
	defer procCloseHandle.Call(handle)

	is32Bit := isTargetProcess32Bit(syscall.Handle(handle))
	return readCmdLine(syscall.Handle(handle), is32Bit)
}

// isTargetProcess32Bit 判断目标进程是否为 32 位（WOW64）进程。
// 在 64 位主程序下，用 NtQueryInformationProcess(ProcessWow64Information)：
// 若返回非零，说明目标运行在 WOW64 下（32 位）。
func isTargetProcess32Bit(handle syscall.Handle) bool {
	var wow64 uintptr
	ret, _, _ := procNtQueryInformationProcess.Call(
		uintptr(handle),
		uintptr(processWow64Info),
		uintptr(unsafe.Pointer(&wow64)),
		uintptr(unsafe.Sizeof(wow64)),
		uintptr(0),
	)
	// NT_SUCCESS(ret) 即 int32(ret) >= 0（int32 截断保留位模式，失败码为负）
	if int32(ret) >= 0 && wow64 != 0 {
		return true
	}
	return false
}

// queryPebAddress 定位目标进程的 PEB 地址（64 位主程序读目标）。
func queryPebAddress(handle syscall.Handle, is32BitTarget bool) (uint64, bool) {
	if is32BitTarget {
		// 64 位主程序读 32 位目标：ProcessWow64Information 直接返回 32 位 PEB 地址
		var wow64 uint32
		ret, _, _ := procNtQueryInformationProcess.Call(
			uintptr(handle),
			uintptr(processWow64Info),
			uintptr(unsafe.Pointer(&wow64)),
			uintptr(unsafe.Sizeof(wow64)),
			uintptr(0),
		)
		if int32(ret) >= 0 && wow64 != 0 {
			return uint64(wow64), true
		}
		return 0, false
	}
	// 64 位主程序读 64 位目标：ProcessBasicInformation 取 PebBaseAddress
	var info processBasicInformation64
	ret, _, _ := procNtQueryInformationProcess.Call(
		uintptr(handle),
		uintptr(processBasicInfo),
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Sizeof(info)),
		uintptr(0),
	)
	if int32(ret) >= 0 && info.PebBaseAddress != 0 {
		return info.PebBaseAddress, true
	}
	return 0, false
}

// readCmdLine 读取目标进程的 CommandLine 字符串。
func readCmdLine(handle syscall.Handle, is32BitTarget bool) string {
	pebAddr, ok := queryPebAddress(handle, is32BitTarget)
	if !ok {
		return ""
	}

	// 读 PEB，取 ProcessParameters 指针
	if is32BitTarget {
		pebBuf := readMemory(handle, pebAddr, uint(unsafe.Sizeof(processEnvironmentBlock32{})))
		if len(pebBuf) != int(unsafe.Sizeof(processEnvironmentBlock32{})) {
			return ""
		}
		peb := (*processEnvironmentBlock32)(unsafe.Pointer(&pebBuf[0]))
		paramsBuf := readMemory(handle, uint64(peb.ProcessParameters), uint(unsafe.Sizeof(rtlUserProcessParameters32{})))
		if len(paramsBuf) != int(unsafe.Sizeof(rtlUserProcessParameters32{})) {
			return ""
		}
		params := (*rtlUserProcessParameters32)(unsafe.Pointer(&paramsBuf[0]))
		if params.CommandLineLength == 0 {
			return ""
		}
		cmdBuf := readMemory(handle, uint64(params.CommandLineAddress), uint(params.CommandLineLength))
		return utf16BytesToString(cmdBuf)
	}

	// 64 位目标
	pebBuf := readMemory(handle, pebAddr, uint(unsafe.Sizeof(processEnvironmentBlock64{})))
	if len(pebBuf) != int(unsafe.Sizeof(processEnvironmentBlock64{})) {
		return ""
	}
	peb := (*processEnvironmentBlock64)(unsafe.Pointer(&pebBuf[0]))
	paramsBuf := readMemory(handle, peb.ProcessParameters, uint(unsafe.Sizeof(rtlUserProcessParameters64{})))
	if len(paramsBuf) != int(unsafe.Sizeof(rtlUserProcessParameters64{})) {
		return ""
	}
	params := (*rtlUserProcessParameters64)(unsafe.Pointer(&paramsBuf[0]))
	if params.CommandLineLength == 0 {
		return ""
	}
	cmdBuf := readMemory(handle, params.CommandLineAddress, uint(params.CommandLineLength))
	return utf16BytesToString(cmdBuf)
}

// readMemory 用 NtReadVirtualMemory 读取目标进程内存。
// 失败返回 nil（与 gopsutil readProcessMemory 行为一致）。
func readMemory(handle syscall.Handle, address uint64, size uint) []byte {
	var read uint
	buffer := make([]byte, size)
	ret, _, _ := procNtReadVirtualMemory.Call(
		uintptr(handle),
		uintptr(address),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(size),
		uintptr(unsafe.Pointer(&read)),
	)
	if int32(ret) >= 0 && read > 0 {
		return buffer[:read]
	}
	return nil
}

// utf16BytesToString 把 UTF-16LE 字节序列转为 Go 字符串。
// （ReadProcessMemory 读出来的是字节，需重新组装成 []uint16 再转字符串）
func utf16BytesToString(src []byte) string {
	if len(src) < 2 {
		return ""
	}
	srcLen := len(src) / 2
	codePoints := make([]uint16, srcLen)
	for i := 0; i < srcLen; i++ {
		codePoints[i] = uint16(src[2*i]) | uint16(src[2*i+1])<<8
	}
	return syscall.UTF16ToString(codePoints)
}
