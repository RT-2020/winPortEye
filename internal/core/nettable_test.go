package core

import (
	"testing"
)

// 注：原对照测试（vs gopsutil，步骤 3 验证原生枚举 617 条 vs gopsutil 617 条 0 差异）
// 已完成使命。gopsutil 依赖移除后（步骤 6），这里保留自洽的结构/映射验证。

// TestEnumerateAll 返回非空连接列表（本机必然有 TCP/UDP 端点）。
func TestEnumerateAll(t *testing.T) {
	conns, err := ListConnections(KindAll)
	if err != nil {
		t.Fatalf("ListConnections 失败: %v", err)
	}
	if len(conns) == 0 {
		t.Error("KindAll 应返回非空连接列表（本机必有端口）")
	}
	// 抽查：所有连接的 Protocol 应是合法值、State 非空（UDP→NONE）
	for i, c := range conns {
		if c.Protocol != ProtocolTCP && c.Protocol != ProtocolUDP {
			t.Errorf("连接 %d 协议非法: %q", i, c.Protocol)
		}
		if c.State == "" {
			t.Errorf("连接 %d State 不应为空（UDP 应兜底成 NONE）: %+v", i, c)
		}
		if c.LocalPort == 0 {
			t.Errorf("连接 %d LocalPort 不应为 0: %+v", i, c)
		}
	}
	t.Logf("枚举到 %d 条连接", len(conns))
}

// TestEnumerateTcpOnly 验证 KindTCP 只返回 TCP。
func TestEnumerateTcpOnly(t *testing.T) {
	conns, err := ListConnections(KindTCP)
	if err != nil {
		t.Fatalf("ListConnections(KindTCP) 失败: %v", err)
	}
	for i, c := range conns {
		if c.Protocol != ProtocolTCP {
			t.Errorf("KindTCP 结果 %d 应全是 TCP，got %q", i, c.Protocol)
		}
	}
}

// TestEnumerateUdpOnly 验证 KindUDP 只返回 UDP，且 State 全为 NONE。
func TestEnumerateUdpOnly(t *testing.T) {
	conns, err := ListConnections(KindUDP)
	if err != nil {
		t.Fatalf("ListConnections(KindUDP) 失败: %v", err)
	}
	for i, c := range conns {
		if c.Protocol != ProtocolUDP {
			t.Errorf("KindUDP 结果 %d 应全是 UDP，got %q", i, c.Protocol)
		}
		if c.State != "NONE" {
			t.Errorf("UDP 连接 %d State 应为 NONE，got %q", i, c.State)
		}
	}
}

// TestTcpStateMappingComplete 验证 TCP 状态映射逐值复刻（审查 Issue 3）。
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
