package core

import (
	"fmt"
)

// FilterKind 控制端口枚举的协议/地址族过滤。
// 取值："all" / "tcp" / "tcp4" / "tcp6" / "udp" / "udp4" / "udp6"
// 语义与 gopsutil net.Connections 的 kind 参数完全一致（已逐值复刻）。
type FilterKind string

const (
	KindAll  FilterKind = "all"
	KindTCP  FilterKind = "tcp"
	KindTCP4 FilterKind = "tcp4"
	KindTCP6 FilterKind = "tcp6"
	KindUDP  FilterKind = "udp"
	KindUDP4 FilterKind = "udp4"
	KindUDP6 FilterKind = "udp6"
)

// ListConnections 枚举网络连接，并关联占用进程信息。
// kind 控制 TCP/UDP × IPv4/IPv6 的过滤，传 KindAll 枚举全部。
//
// 底层用 GetExtendedTcpTable/GetExtendedUdpTable（替换 gopsutil），
// 协议类型由「来自哪张表」隐式确定（TCP 表→ProtocolTCP，UDP 表→ProtocolUDP），
// 不再有 c.Type 判定（原生 API 行结构无 Type 字段）。
func ListConnections(kind FilterKind) ([]Connection, error) {
	rawConns, err := enumerateConnections(kind)
	if err != nil {
		return nil, fmt.Errorf("枚举连接失败: %w", err)
	}

	// 进程名/路径缓存：同一次扫描中多个端口常属同一进程，避免重复 OpenProcess
	// 用 context 复用 []uint16 缓冲区 + PID 缓存，进一步降低分配
	q := newProcQueryContext()

	result := make([]Connection, 0, len(rawConns))
	for _, c := range rawConns {
		conn := Connection{
			Protocol:   c.protocol, // 由表决定：TCP 表→tcp，UDP 表→udp
			LocalAddr:  c.localAddr,
			LocalPort:  c.localPort,
			RemoteAddr: c.remoteAddr,
			RemotePort: c.remotePort,
			State:      c.state, // TCP 状态字符串；UDP 为空串
			Pid:        c.pid,
		}
		// UDP 行 state 为空、TCP 未知状态也为空 → 统一兜底成 "NONE"
		if conn.State == "" {
			conn.State = "NONE"
		}
		if c.pid > 0 {
			conn.ProcessName, conn.ProcessPath = q.namePath(c.pid)
		}
		result = append(result, conn)
	}
	return result, nil
}

// FindPort 查找占用指定本地端口的全部连接。
// 返回所有 localPort == port 的连接（一个端口可能被多个进程/连接占用）。
func FindPort(port uint16, kind FilterKind) ([]Connection, error) {
	conns, err := ListConnections(kind)
	if err != nil {
		return nil, err
	}
	matched := make([]Connection, 0)
	for _, c := range conns {
		if c.LocalPort == port {
			matched = append(matched, c)
		}
	}
	return matched, nil
}
