package core

import (
	"fmt"

	psnet "github.com/shirou/gopsutil/v4/net"
)

// FilterKind 控制端口枚举的协议/地址族过滤。
// 取值："all" / "tcp" / "tcp4" / "tcp6" / "udp" / "udp4" / "udp6"
// 对应 gopsutil net.Connections 的 kind 参数。
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
// 注意：单次 net.Connections("tcp") 已同时返回 IPv4+IPv6，
// 因此这里把 kind 拆成需要的组合调用，避免重复查表。
func ListConnections(kind FilterKind) ([]Connection, error) {
	// gopsutil 的 kind 直接支持组合，无需手动拼
	rawConns, err := psnet.Connections(string(kind))
	if err != nil {
		return nil, fmt.Errorf("枚举连接失败: %w", err)
	}

	// 进程名/路径缓存：同一次扫描中多个端口常属同一进程，避免重复 OpenProcess
	// 合并缓存：gopsutil 的 Name()=Base(Exe())，一次 Exe 调用即可同时拿到 name 和 path
	procCache := make(map[int32]procInfoCache, 128)

	result := make([]Connection, 0, len(rawConns))
	for _, c := range rawConns {
		// 判定协议：根据 Type（SOCK_STREAM=TCP / SOCK_DGRAM=UDP）
		proto := ProtocolTCP
		if c.Type == 2 { // SOCK_DGRAM
			proto = ProtocolUDP
		}

		conn := Connection{
			Protocol:   proto,
			LocalAddr:  c.Laddr.IP,
			LocalPort:  uint16(c.Laddr.Port),
			RemoteAddr: c.Raddr.IP,
			RemotePort: uint16(c.Raddr.Port),
			State:      c.Status,
			Pid:        c.Pid,
		}
		if conn.State == "" {
			conn.State = "NONE"
		}
		if c.Pid > 0 {
			conn.ProcessName, conn.ProcessPath = getProcessNamePath(c.Pid, procCache)
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
