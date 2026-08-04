package core

import (
	"path/filepath"
	"testing"

	gopsprocess "github.com/shirou/gopsutil/v4/process"
)

// TestNamePathMatchesGopsutil 验证原生 QueryFullProcessImageNameW 查出的
// 进程名/路径与 gopsutil 完全一致（步骤 2 的对照验证）。
//
// 取当前进程自身作为样本：它必然存在、必然可读、无权限问题。
func TestNamePathMatchesGopsutil(t *testing.T) {
	// 当前测试进程的 PID
	pid := int32(getCurrentPID())
	if pid <= 0 {
		t.Fatal("无法获取当前 PID")
	}

	q := newProcQueryContext()
	name, path := q.namePath(pid)

	// gopsutil 参照值
	gp, err := gopsprocess.NewProcess(pid)
	if err != nil {
		t.Fatalf("gopsutil NewProcess 失败: %v", err)
	}
	gopsName, _ := gp.Name()
	gopsExe, _ := gp.Exe()

	// 完整路径必须一致（同一 API，必须完全相同）
	if path != gopsExe {
		t.Errorf("完整路径不一致:\n  winapi:  %q\n  gopsutil: %q", path, gopsExe)
	}

	// 进程名必须一致
	if name != gopsName {
		// 容错：gopsutil 的 Name 有时会做额外处理，退化检查 Base(Exe)
		wantBase := filepath.Base(gopsExe)
		if name != wantBase {
			t.Errorf("进程名不一致:\n  winapi:  %q\n  gopsutil: %q\n  Base(Exe): %q", name, gopsName, wantBase)
		}
	}

	// 非空检查：自身进程必然能查到
	if path == "" {
		t.Error("当前进程的完整路径不应为空")
	}
	if name == "" {
		t.Error("当前进程的进程名不应为空")
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
	// PID 0 和 -1 应返回空
	if name, path := q.namePath(0); name != "" || path != "" {
		t.Errorf("PID 0 应返回空，got name=%q path=%q", name, path)
	}
	if name, path := q.namePath(-1); name != "" || path != "" {
		t.Errorf("PID -1 应返回空，got name=%q path=%q", name, path)
	}
	// 不存在的超大 PID 应优雅返回空（不 panic）
	if name, path := q.namePath(99999999); name != "" || path != "" {
		t.Logf("PID 99999999 返回 name=%q path=%q（非空也可能是系统复用，仅记录）", name, path)
	}
}
