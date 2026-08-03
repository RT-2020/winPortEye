package core

import (
	"github.com/shirou/gopsutil/v4/process"
)

// GetProcessInfo 查询指定 PID 的进程详细信息。
// 对部分系统进程，Name/Exe 可能因权限不足返回空，这是正常现象。
func GetProcessInfo(pid int32) (ProcessInfo, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ProcessInfo{}, err
	}
	// 各字段独立取，权限不足时忽略单字段错误，尽量返回能拿到的信息
	name, _ := p.Name()
	exe, _ := p.Exe()
	cmdline, _ := p.Cmdline()
	ctime, _ := p.CreateTime()
	return ProcessInfo{
		Pid:         pid,
		Name:        name,
		Path:        exe,
		CommandLine: cmdline,
		CreateTime:  ctime,
	}, nil
}

// 批量场景下按 PID 查进程名（轻量版），内部带缓存避免重复 OpenProcess。
func getProcessName(pid int32, cache map[int32]string) string {
	if pid <= 0 {
		return ""
	}
	if name, ok := cache[pid]; ok {
		return name
	}
	name := ""
	if p, err := process.NewProcess(pid); err == nil {
		if n, err := p.Name(); err == nil {
			name = n
		}
	}
	cache[pid] = name
	return name
}

// 批量场景下按 PID 查进程路径（轻量版），内部带缓存。
func getProcessPath(pid int32, cache map[int32]string) string {
	if pid <= 0 {
		return ""
	}
	if p, ok := cache[pid]; ok {
		return p
	}
	path := ""
	if p, err := process.NewProcess(pid); err == nil {
		if e, err := p.Exe(); err == nil {
			path = e
		}
	}
	cache[pid] = path
	return path
}
