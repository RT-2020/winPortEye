package core

import "testing"

func TestParseNetshOutput(t *testing.T) {
	// 真实 netsh 输出格式（含中文表头、空行、带 * 的管理端口）
	output := `协议 tcp 端口排除范围

开始端口    结束端口
----------    --------
        80          80
      1333        1432
     8250        8349
   50000       50059     *

* - 管理的端口排除。
`
	ranges := parseNetshOutput(output, "tcp")
	want := []ExcludedRange{
		{Protocol: "tcp", Start: 80, End: 80, Managed: false},
		{Protocol: "tcp", Start: 1333, End: 1432, Managed: false},
		{Protocol: "tcp", Start: 8250, End: 8349, Managed: false},
		{Protocol: "tcp", Start: 50000, End: 50059, Managed: true},
	}
	if len(ranges) != len(want) {
		t.Fatalf("解析数量不符: want %d, got %d (%+v)", len(want), len(ranges), ranges)
	}
	for i, w := range want {
		if ranges[i] != w {
			t.Errorf("row %d: want %+v, got %+v", i, w, ranges[i])
		}
	}
}

func TestParseNetshOutputSkipsInvalidLines(t *testing.T) {
	// 表头、乱码、越界端口应被跳过
	output := `开始端口    结束端口
----------    --------
abc    def
99999  99999
80     80
`
	ranges := parseNetshOutput(output, "tcp")
	if len(ranges) != 1 {
		t.Fatalf("应只解析出 1 行有效数据，got %d (%+v)", len(ranges), ranges)
	}
	if ranges[0].Start != 80 || ranges[0].End != 80 {
		t.Errorf("应为 80-80，got %+v", ranges[0])
	}
}

func TestParseNetshOutputEmpty(t *testing.T) {
	ranges := parseNetshOutput("", "tcp")
	if len(ranges) != 0 {
		t.Errorf("空输入应返回空，got %+v", ranges)
	}
}

// TestParseNetshOutputEnglish 验证英文版 Windows 的 netsh 输出也能正确解析。
// 解析只认"前两个字段是纯数字"的行，对表头文案（中文/英文）天然兼容。
func TestParseNetshOutputEnglish(t *testing.T) {
	output := `Protocol tcp Port Exclusion Ranges

Start Port    End Port
----------    --------
        80          80
      8250        8349
   50000       50059     *

* - Administered port exclusions.`
	ranges := parseNetshOutput(output, "tcp")
	want := []ExcludedRange{
		{Protocol: "tcp", Start: 80, End: 80, Managed: false},
		{Protocol: "tcp", Start: 8250, End: 8349, Managed: false},
		{Protocol: "tcp", Start: 50000, End: 50059, Managed: true},
	}
	if len(ranges) != len(want) {
		t.Fatalf("英文输出解析数量不符: want %d, got %d (%+v)", len(want), len(ranges), ranges)
	}
	for i, w := range want {
		if ranges[i] != w {
			t.Errorf("row %d: want %+v, got %+v", i, w, ranges[i])
		}
	}
}

// TestParseNetshOutputGarbled 验证乱码/损坏的输出不会 panic，静默返回有效部分。
func TestParseNetshOutputGarbled(t *testing.T) {
	output := `some garbage line
80 80
??? ???
completely invalid
443 444`
	ranges := parseNetshOutput(output, "tcp")
	if len(ranges) != 2 {
		t.Fatalf("应解析出 2 行有效数据（跳过乱码），got %d (%+v)", len(ranges), ranges)
	}
	if ranges[0].Start != 80 || ranges[1].Start != 443 {
		t.Errorf("有效行解析错误: %+v", ranges)
	}
}

func TestFindExcludedPort(t *testing.T) {
	ranges := []ExcludedRange{
		{Protocol: "tcp", Start: 8250, End: 8349, Managed: false},
		{Protocol: "udp", Start: 8250, End: 8349, Managed: false},
	}
	// 8317 落在 8250-8349 内，TCP 和 UDP 都命中
	matched := FindExcludedPort(8317, ranges)
	if len(matched) != 2 {
		t.Fatalf("8317 应命中 2 条（tcp+udp），got %d", len(matched))
	}
	// 9000 不在任何范围
	if m := FindExcludedPort(9000, ranges); m != nil {
		t.Errorf("9000 不应命中，got %+v", m)
	}
	// 边界：8250 命中，8249 不命中，8349 命中，8350 不命中
	if len(FindExcludedPort(8250, ranges)) != 2 {
		t.Error("边界 8250 应命中")
	}
	if len(FindExcludedPort(8349, ranges)) != 2 {
		t.Error("边界 8349 应命中")
	}
	if FindExcludedPort(8249, ranges) != nil {
		t.Error("8249 不应命中")
	}
	if FindExcludedPort(8350, ranges) != nil {
		t.Error("8350 不应命中")
	}
}
