package core

import (
	"path/filepath"

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

// procInfoCache 缓存一次扫描中某 PID 的「进程名 + 完整路径」。
// 之所以合并：gopsutil 的 Name() 内部实现就是 filepath.Base(Exe())，
// 原先 getProcessName/getProcessPath 各调一次，导致同一 PID 的完整路径被查了两遍。
// 合并后只查一次 Exe，name 取 Base、path 取完整路径，开销减半。
type procInfoCache struct {
	name string
	path string
}

// getProcessNamePath 一次查询拿到进程名和完整路径，内部带缓存避免重复 OpenProcess。
// 进程名 = filepath.Base(完整路径)；权限不足/系统进程时两者均返回空串（与原行为一致）。
func getProcessNamePath(pid int32, cache map[int32]procInfoCache) (name, path string) {
	if pid <= 0 {
		return "", ""
	}
	if info, ok := cache[pid]; ok {
		return info.name, info.path
	}
	path = ""
	if p, err := process.NewProcess(pid); err == nil {
		if e, err := p.Exe(); err == nil {
			path = e
		}
	}
	name = filepath.Base(path) // path 为空时 Base 返回 "."，需归一化为空串
	if name == "." || name == string(filepath.Separator) {
		name = ""
	}
	cache[pid] = procInfoCache{name: name, path: path}
	return name, path
}
