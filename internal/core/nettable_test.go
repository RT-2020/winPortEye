package core

import (
	"sort"
	"testing"

	psnet "github.com/shirou/gopsutil/v4/net"
)

// TestEnumerateMatchesGopsutil 步骤 3 核心对照验证：
// 原生 GetExtendedTcpTable/UDP 枚举出的连接集合必须与 gopsutil 高度一致。
//
// 用连接指纹（协议+本地地址+端口+远端+PID+State）做集合对比，不看顺序。
// 端口表在两次调用间可能有瞬态变化，允许少量差异，但核心应匹配。
// 这是审查 Issue 2/3/4（字段映射）的最强验证。
func TestEnumerateMatchesGopsutil(t *testing.T) {
	ours, err := ListConnections(KindAll)
	if err != nil {
		t.Fatalf("原生枚举失败: %v", err)
	}

	theirs, err := psnet.Connections("all")
	if err != nil {
		t.Fatalf("gopsutil 枚举失败: %v", err)
	}

	oursSet := make(map[string]int, len(ours))
	for _, c := range ours {
		oursSet[connFingerprint(string(c.Protocol), c.LocalAddr, c.LocalPort, c.RemoteAddr, c.RemotePort, c.Pid, c.State)]++
	}
	theirsSet := make(map[string]int, len(theirs))
	for _, c := range theirs {
		proto := "tcp"
		if c.Type == 2 {
			proto = "udp"
		}
		state := c.Status
		if state == "" {
			state = "NONE"
		}
		theirsSet[connFingerprint(proto, c.Laddr.IP, uint16(c.Laddr.Port), c.Raddr.IP, uint16(c.Raddr.Port), c.Pid, state)]++
	}

	onlyOurs, onlyTheirs := diffSets(oursSet, theirsSet)

	// 允许端口表的瞬态变化（10% + 5 的容差）
	total := len(ours) + len(theirs)
	if len(onlyOurs)+len(onlyTheirs) > total/10+5 {
		t.Errorf("原生 vs gopsutil 差异过大（总 %d 条）:\n  仅原生: %d 样例 %v\n  仅gopsutil: %d 样例 %v",
			total, len(onlyOurs), firstN(onlyOurs, 3), len(onlyTheirs), firstN(onlyTheirs, 3))
	}
	t.Logf("对照统计: 原生=%d gopsutil=%d 仅原生=%d 仅gops=%d（瞬态差异可接受）",
		len(ours), len(theirs), len(onlyOurs), len(onlyTheirs))
}

// TestTcpStateMappingComplete 验证 TCP 状态映射逐值复刻 gopsutil（审查 Issue 3）。
func TestTcpStateMappingComplete(t *testing.T) {
	cases := map[uint32]string{
		0:  "", // UNKNOWN → 空串（由 scanner 兜底 NONE）
		1:  "CLOSED",
		2:  "LISTEN",
		3:  "SYN_SENT",
		4:  "SYN_RECEIVED",
		5:  "ESTABLISHED",
		6:  "FIN_WAIT_1",
		7:  "FIN_WAIT_2",
		8:  "CLOSE_WAIT",
		9:  "CLOSING",
		10: "LAST_ACK",
		11: "TIME_WAIT",
		12: "DELETE",
		99: "", // 越界 → 空串
	}
	for state, expected := range cases {
		if got := tcpStateToString(state); got != expected {
			t.Errorf("state %d: want %q, got %q", state, expected, got)
		}
	}
}

// TestIPv4String 验证 IPv4 地址解析（网络字节序小端存储）。
func TestIPv4String(t *testing.T) {
	// 0x0100007F 内存小端为 7F 00 00 01 → "127.0.0.1"
	if got := ipv4String(0x0100007F); got != "127.0.0.1" {
		t.Errorf("ipv4String(0x0100007F) = %q, want 127.0.0.1", got)
	}
	if got := ipv4String(0); got != "0.0.0.0" {
		t.Errorf("ipv4String(0) = %q, want 0.0.0.0", got)
	}
}

// TestNtohs 验证端口字节序反转。
// 端口 80 网络字节序（大端）= 0x0050，小端内存存为 0x5000。
func TestNtohs(t *testing.T) {
	if got := ntohs(0x5000); got != 80 {
		t.Errorf("ntohs(0x5000) = %d, want 80", got)
	}
}

// --- 辅助函数 ---

func connFingerprint(proto, lAddr string, lPort uint16, rAddr string, rPort uint16, pid int32, state string) string {
	return proto + "|" + lAddr + ":" + itoa32(int(lPort)) + "->" + rAddr + ":" + itoa32(int(rPort)) + "|pid=" + itoa32(int(pid)) + "|" + state
}

func diffSets(a, b map[string]int) (onlyA, onlyB []string) {
	for k := range a {
		if _, ok := b[k]; !ok {
			onlyA = append(onlyA, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			onlyB = append(onlyB, k)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func itoa32(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [12]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
