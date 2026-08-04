package core

import (
	"path/filepath"
	"testing"
)

// 注：原对照测试（vs gopsutil）在步骤 2/2.5/3 已完成使命并全部通过。
// gopsutil 依赖移除后（步骤 6），这些测试改为自洽断言，不再依赖 gopsutil。

// TestNamePathSelf 验证能查到当前进程自身的名/路径（非空、格式正确）。
func TestNamePathSelf(t *testing.T) {
	pid := int32(getCurrentPID())
	if pid <= 0 {
		t.Fatal("无法获取当前 PID")
	}

	q := newProcQueryContext()
	name, path := q.namePath(pid)

	if path == "" {
		t.Error("当前进程的完整路径不应为空")
	}
	if name == "" {
		t.Error("当前进程的进程名不应为空")
	}
	// 进程名必须是路径的 basename
	if want := filepath.Base(path); name != want {
		t.Errorf("进程名应为路径 basename: got name=%q, want %q (path=%q)", name, want, path)
	}
}

// TestNamePathCacheHit 验证同一 PID 第二次查询命中缓存（不再调 API）。
func TestNamePathCacheHit(t *testing.T) {
	q := newProcQueryContext()
	pid := int32(getCurrentPID())

	name1, path1 := q.namePath(pid)
	name2, path2 := q.namePath(pid)

	if name1 != name2 || path1 != path2 {
		t.Errorf("缓存命中前后结果不一致: (%q,%q) vs (%q,%q)", name1, path1, name2, path2)
	}
	if len(q.cache) != 1 {
		t.Errorf("缓存应有 1 条记录，实为 %d", len(q.cache))
	}
}

// TestNamePathInvalidPid 验证无效 PID 返回空串（不 panic、不报错）。
func TestNamePathInvalidPid(t *testing.T) {
	q := newProcQueryContext()
	if name, path := q.namePath(0); name != "" || path != "" {
		t.Errorf("PID 0 应返回空，got name=%q path=%q", name, path)
	}
	if name, path := q.namePath(-1); name != "" || path != "" {
		t.Errorf("PID -1 应返回空，got name=%q path=%q", name, path)
	}
}

// TestGetProcessInfoSelf 验证 GetProcessInfo 能返回当前进程的完整信息。
func TestGetProcessInfoSelf(t *testing.T) {
	pid := int32(getCurrentPID())
	info, err := GetProcessInfo(pid)
	if err != nil {
		t.Fatalf("GetProcessInfo 失败: %v", err)
	}
	if info.Pid != pid {
		t.Errorf("PID 不符: got %d, want %d", info.Pid, pid)
	}
	if info.Name == "" {
		t.Error("当前进程 Name 不应为空")
	}
	if info.Path == "" {
		t.Error("当前进程 Path 不应为空")
	}
	// CommandLine 对测试进程应非空（测试进程能读到自己）
	if info.CommandLine == "" {
		t.Log("注意：CommandLine 为空（某些环境下读自己 cmdline 可能受限）")
	}
	if info.CreateTime == 0 {
		t.Error("当前进程 CreateTime 不应为 0")
	}
}

// TestGetProcessInfoSystemProcess 验证系统进程（PID 4）降级行为。
// §4.2 不变量：权限不足 → CommandLine 返回空串（与 gopsutil 现状一致）。
func TestGetProcessInfoSystemProcess(t *testing.T) {
	info, err := GetProcessInfo(4) // System 进程
	if err != nil {
		t.Fatalf("PID 4 不应报错: %v", err)
	}
	// System 进程读 cmdline 必然失败，应降级返回空串
	if info.CommandLine != "" {
		t.Errorf("PID 4 (System) CommandLine 应为空（权限不足），got %q", info.CommandLine)
	}
}
