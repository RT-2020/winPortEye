package ui

import (
	"reflect"
	"testing"
)

// TestResolveRightClickSelectionBlankArea 右键落在空白区（idx<0）：
// 应原样返回当前选中集合，不动用户已建立的选中状态。
func TestResolveRightClickSelectionBlankArea(t *testing.T) {
	cur := []int{1, 3, 5}
	got := resolveRightClickSelection(-1, cur)
	if !reflect.DeepEqual(got, cur) {
		t.Fatalf("空白区应保持原选中: want %v, got %v", cur, got)
	}
}

// TestResolveRightClickSelectionAlreadySelected 右键落在已选中行：
// 应保持多选不变，绝不能把多选缩成单选。
func TestResolveRightClickSelectionAlreadySelected(t *testing.T) {
	cur := []int{1, 3, 5}
	// 右键命中其中任一已选行，结果都应等于原多选集合
	for _, hit := range cur {
		got := resolveRightClickSelection(hit, cur)
		if !reflect.DeepEqual(got, cur) {
			t.Fatalf("命中已选中行 %d 应保持多选: want %v, got %v", hit, cur, got)
		}
	}
}

// TestResolveRightClickSelectionSingle 右键落在未选中行：
// 应单选该行，丢弃原选中，使后续菜单动作明确针对这一行。
func TestResolveRightClickSelectionSingle(t *testing.T) {
	cur := []int{1, 3, 5}
	got := resolveRightClickSelection(2, cur)
	want := []int{2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("命中未选中行应单选该行: want %v, got %v", want, got)
	}
}

// TestResolveRightClickSelectionTable 表驱动覆盖三类规则的典型场景，
// 兼顾空选中、单选、多选、命中头/中/尾行等边界。
func TestResolveRightClickSelectionTable(t *testing.T) {
	cases := []struct {
		name     string
		idx      int
		selected []int
		want     []int
	}{
		{"空白区_空选中", -1, nil, nil},
		{"空白区_有选中", -1, []int{0, 2}, []int{0, 2}},
		{"命中已选_单选首行", 0, []int{0}, []int{0}},
		{"命中已选_多选中段", 3, []int{1, 3, 5}, []int{1, 3, 5}},
		{"命中已选_多选末行", 5, []int{1, 3, 5}, []int{1, 3, 5}},
		{"命中未选_空选中改单选", 4, nil, []int{4}},
		{"命中未选_多选改单选", 2, []int{1, 3, 5}, []int{2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveRightClickSelection(c.idx, c.selected)
			if !intsEqual(got, c.want) {
				t.Fatalf("idx=%d selected=%v: want %v, got %v", c.idx, c.selected, c.want, got)
			}
		})
	}
}

// TestIntsEqual 校验选中变更判断辅助函数本身。
func TestIntsEqual(t *testing.T) {
	if !intsEqual(nil, nil) {
		t.Error("nil == nil 应为 true")
	}
	if !intsEqual([]int{1, 2, 3}, []int{1, 2, 3}) {
		t.Error("相同内容应为 true")
	}
	if intsEqual([]int{1, 2}, []int{1, 2, 3}) {
		t.Error("不同长度应为 false")
	}
	if intsEqual([]int{1, 2, 3}, []int{1, 3, 2}) {
		t.Error("顺序不同应为 false")
	}
}

// TestClassifyTargets 校验杀进程前的目标分类：
// explorer → 提示级；csrss → 危险级；普通进程 → 两空；PID 4 → 危险级；空名 → 危险级。
func TestClassifyTargets(t *testing.T) {
	cases := []struct {
		name          string
		targets       []ProcessGroupRow
		wantDangerous []string
		wantCaution   []string
	}{
		{"explorer_提示级", []ProcessGroupRow{{Pid: 4321, ProcessName: "explorer.exe"}},
			nil, []string{"PID 4321 (explorer.exe)"}},
		{"explorer_无后缀同样命中", []ProcessGroupRow{{Pid: 4321, ProcessName: "EXPLORER"}},
			nil, []string{"PID 4321 (EXPLORER)"}},
		{"csrss_危险级", []ProcessGroupRow{{Pid: 600, ProcessName: "csrss.exe"}},
			[]string{"PID 600 (csrss.exe)"}, nil},
		{"普通进程_两空", []ProcessGroupRow{{Pid: 100, ProcessName: "notepad.exe"}},
			nil, nil},
		{"PID4_危险级", []ProcessGroupRow{{Pid: 4, ProcessName: "System"}},
			[]string{"PID 4 (System)"}, nil},
		{"空名_危险级", []ProcessGroupRow{{Pid: 700, ProcessName: ""}},
			[]string{"PID 700 (未知进程)"}, nil},
		{"混合_分类正确", []ProcessGroupRow{
			{Pid: 4, ProcessName: "System"},
			{Pid: 600, ProcessName: "csrss.exe"},
			{Pid: 4321, ProcessName: "explorer.exe"},
			{Pid: 100, ProcessName: "notepad.exe"},
		}, []string{"PID 4 (System)", "PID 600 (csrss.exe)"}, []string{"PID 4321 (explorer.exe)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dangerous, caution := classifyTargets(c.targets)
			if !reflect.DeepEqual(dangerous, c.wantDangerous) {
				t.Fatalf("dangerous: want %v, got %v", c.wantDangerous, dangerous)
			}
			if !reflect.DeepEqual(caution, c.wantCaution) {
				t.Fatalf("caution: want %v, got %v", c.wantCaution, caution)
			}
		})
	}
}
