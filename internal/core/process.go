package core

import (
	"path/filepath"
	"unsafe"
)

// GetProcessInfo 查询指定 PID 的进程详细信息。
// 对部分系统进程，Name/Exe/CommandLine 可能因权限不足返回空，这是正常现象。
//
// 全部字段已改用原生 Win32 API：
//   - Name/Path: QueryFullProcessImageNameW
//   - CommandLine: PEB 遍历（cmdline.go）
//   - CreateTime: GetProcessTimes
//   - ParentPid: NtQueryInformationProcess（尽力而为，失败保持 0）
func GetProcessInfo(pid int32) (ProcessInfo, error) {
	// Name/Path：原生 API（一次查询拿两个值）
	q := newProcQueryContext()
	name, path := q.namePath(pid)

	info := ProcessInfo{
		Pid:         pid,
		Name:        name,
		Path:        path,
		CommandLine: getProcessCommandLine(pid), // PEB 遍历，权限不足返回空串
		CreateTime:  getProcessCreateTime(pid),   // GetProcessTimes，权限不足返回 0
	}

	// 父进程 PID：NtQueryInformationProcess(ProcessBasicInformation) 返回
	// InheritedFromUniqueProcessId。尽力而为：OpenProcess 或查询失败保持 0。
	if pid > 0 {
		handle, _, _ := procOpenProcess.Call(
			uintptr(processQueryInformation), // Nt 查询需要 PROCESS_QUERY_INFORMATION
			uintptr(0),
			uintptr(pid),
		)
		if handle != 0 {
			var pbi processBasicInformation64
			ret, _, _ := procNtQueryInformationProcess.Call(
				uintptr(handle),
				uintptr(processBasicInfo),
				uintptr(unsafe.Pointer(&pbi)),
				uintptr(unsafe.Sizeof(pbi)),
				uintptr(0),
			)
			if int(ret) >= 0 { // NT_SUCCESS
				info.ParentPid = int32(pbi.InheritedFromUniqueProcessId)
			}
			procCloseHandle.Call(handle)
		}
	}

	return info, nil
}

// procInfoCache 缓存一次扫描中某 PID 的「进程名 + 完整路径」。
// 之所以合并：gopsutil 的 Name() 内部实现就是 filepath.Base(Exe())，
// 原先 getProcessName/getProcessPath 各调一次，导致同一 PID 的完整路径被查了两遍。
// 合并后只查一次 Exe，name 取 Base、path 取完整路径，开销减半。
type procInfoCache struct {
	name string
	path string
}

// procQueryContext 是一次扫描的进程查询上下文，持有可复用的缓冲区和 PID 缓存。
// 缓冲区复用避免每次查询都 make([]uint16, 520)；缓存避免同一 PID 重复 OpenProcess。
// scanner 每次 reload 创建一个，扫描结束丢弃。
type procQueryContext struct {
	buf   []uint16                    // QueryFullProcessImageNameW 的输出缓冲，跨 PID 复用
	cache map[int32]procInfoCache     // PID -> {name, path} 缓存
}

// newProcQueryContext 创建一次扫描的查询上下文。
func newProcQueryContext() *procQueryContext {
	return &procQueryContext{
		buf:   make([]uint16, 520), // MAX_LONG_PATH
		cache: make(map[int32]procInfoCache, 128),
	}
}

// namePath 一次查询拿到进程名和完整路径，内部带缓存避免重复 OpenProcess。
// 进程名 = filepath.Base(完整路径)；权限不足/系统进程时两者均返回空串（与原行为一致）。
func (q *procQueryContext) namePath(pid int32) (name, path string) {
	if pid <= 0 {
		return "", ""
	}
	if info, ok := q.cache[pid]; ok {
		return info.name, info.path
	}
	// 用原生 QueryFullProcessImageNameW 替代 gopsutil 的 Exe()，
	// 去掉 gopsutil 的对象/context/错误包装开销（77.8% 分配大头来源）
	path, _ = queryProcessImagePath(pid, q.buf)
	name = filepath.Base(path) // path 为空时 Base 返回 "."，需归一化为空串
	if name == "." || name == string(filepath.Separator) {
		name = ""
	}
	q.cache[pid] = procInfoCache{name: name, path: path}
	return name, path
}
