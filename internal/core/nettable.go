// Package core 的 nettable.go：用 GetExtendedTcpTable/GetExtendedUdpTable 枚举网络连接。
//
// 替换 gopsutil 的 psnet.Connections（原占 16.5% 分配）。
// 底层调用与 gopsutil 完全相同的 API（iphlpapi.dll），去掉中间层开销。
//
// 关键实现要点（已对照 gopsutil v4.24.8 net_windows.go 逐值复刻）：
//   - kind 语义：all=TCP4+TCP6+UDP4+UDP6（4 张表）；tcp=TCP4+TCP6 等
//   - tableClass：TCP 用 TCP_TABLE_OWNER_PID_ALL(5)，UDP 用 UDP_TABLE_OWNER_PID(1)
//   - IPv4 地址：网络字节序小端存储，按 %d.%d.%d.%d 还原
//   - 端口：syscall.Ntohs 反转字节序
//   - TCP 状态：逐值复刻 gopsutil tcpStatuses（12 项）
//   - UDP：MIB_UDPROW 无 state 字段，State 留空（由 scanner.go 兜底成 "NONE"）
package core

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

var (
	modiphlpapi                 = syscall.NewLazyDLL("iphlpapi.dll")
	procGetExtendedTcpTable     = modiphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdpTable     = modiphlpapi.NewProc("GetExtendedUdpTable")
)

// tableClass 常量（GetExtendedTcpTable/UDP 的第 5 个参数）。
const (
	tcpTableOwnerPidAll = 5 // TCP_TABLE_OWNER_PID_ALL：所有 TCP 连接 + PID
	udpTableOwnerPid    = 1 // UDP_TABLE_OWNER_PID：UDP 端点 + PID
)

// 地址族（GetExtendedTcpTable/UDP 的第 4 个参数）。
const (
	afINET  = syscall.AF_INET
	afINET6 = syscall.AF_INET6
)

// rawConn 是从原生 API 表里解析出的一条原始连接（未含进程信息）。
// 进程名/路径由 scanner 层用 procQueryContext 补充。
type rawConn struct {
	protocol   Protocol
	localAddr  string
	localPort  uint16
	remoteAddr string
	remotePort uint16
	state      string // TCP 状态字符串；UDP 为空串
	pid        int32
}

// ---- MIB 结构定义（照 gopsutil，内存布局必须与 Win32 一致）----

// MIB_TCPROW_OWNER_PID：IPv4 TCP 连接行
type mibTCPRowOwnerPid struct {
	dwState      uint32
	dwLocalAddr  uint32
	dwLocalPort  uint32
	dwRemoteAddr uint32
	dwRemotePort uint32
	dwOwningPid  uint32
}

// MIB_TCP6ROW_OWNER_PID：IPv6 TCP 连接行
type mibTCP6RowOwnerPid struct {
	ucLocalAddr     [16]byte
	dwLocalScopeId  uint32
	dwLocalPort     uint32
	ucRemoteAddr    [16]byte
	dwRemoteScopeId uint32
	dwRemotePort    uint32
	dwState         uint32
	dwOwningPid     uint32
}

// MIB_UDPROW_OWNER_PID：IPv4 UDP 端点行（无 state/remote 字段）
type mibUDPRowOwnerPid struct {
	dwLocalAddr uint32
	dwLocalPort uint32
	dwOwningPid uint32
}

// MIB_UDP6ROW_OWNER_PID：IPv6 UDP 端点行
type mibUDP6RowOwnerPid struct {
	ucLocalAddr    [16]byte
	dwLocalScopeId uint32
	dwLocalPort    uint32
	dwOwningPid    uint32
}

// tcpStateStrings 逐值复刻 gopsutil tcpStatuses（MIB_TCP_STATE → 字符串）。
// state 0（UNKNOWN）及任何未匹配值 → 返回 ""（由 scanner.go 兜底成 "NONE"）。
var tcpStateStrings = [...]string{
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
}

// tcpStateToString 把 MIB_TCP_STATE 数值映射为字符串，未知值返回空串。
func tcpStateToString(state uint32) string {
	if state < uint32(len(tcpStateStrings)) {
		return tcpStateStrings[state]
	}
	return ""
}

// enumerateConnections 按协议族组合枚举网络连接。
// kind 决定枚举哪些表（见 FilterKind 注释）。返回原始连接切片。
func enumerateConnections(kind FilterKind) ([]rawConn, error) {
	// kind → 要枚举的表族（与 gopsutil netConnectionKindMap 完全一致）
	var tables []tableSpec
	switch kind {
	case KindAll:
		tables = []tableSpec{{afINET, false}, {afINET6, false}, {afINET, true}, {afINET6, true}}
	case KindTCP:
		tables = []tableSpec{{afINET, false}, {afINET6, false}}
	case KindTCP4:
		tables = []tableSpec{{afINET, false}}
	case KindTCP6:
		tables = []tableSpec{{afINET6, false}}
	case KindUDP:
		tables = []tableSpec{{afINET, true}, {afINET6, true}}
	case KindUDP4:
		tables = []tableSpec{{afINET, true}}
	case KindUDP6:
		tables = []tableSpec{{afINET6, true}}
	default:
		return nil, fmt.Errorf("未知 FilterKind: %s", kind)
	}

	var result []rawConn
	for _, t := range tables {
		var conns []rawConn
		var err error
		if t.udp {
			conns, err = getUDPTable(t.family)
		} else {
			conns, err = getTCPTable(t.family)
		}
		if err != nil {
			return nil, err
		}
		result = append(result, conns...)
	}
	return result, nil
}

// tableSpec 描述一张要枚举的表。
type tableSpec struct {
	family uint32 // AF_INET / AF_INET6
	udp    bool   // true=UDP 表，false=TCP 表
}

// getTCPTable 枚举指定族（v4/v6）的 TCP 连接表。
func getTCPTable(family uint32) ([]rawConn, error) {
	buf, err := queryExtendedTable(family, true) // true=TCP
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, nil
	}

	// 表头第一个字段是 dwNumEntries
	numEntries := *(*uint32)(unsafe.Pointer(&buf[0]))
	if numEntries == 0 {
		return nil, nil
	}

	result := make([]rawConn, 0, numEntries)
	switch family {
	case afINET:
		// 每行 mibTCPRowOwnerPid（6 个 uint32 = 24 字节），紧跟在 dwNumEntries 后
		const rowSize = unsafe.Sizeof(mibTCPRowOwnerPid{})
		offset := unsafe.Sizeof(uint32(0)) // 跳过 dwNumEntries
		for i := uint32(0); i < numEntries; i++ {
			row := (*mibTCPRowOwnerPid)(unsafe.Pointer(&buf[offset]))
			result = append(result, rawConn{
				protocol:   ProtocolTCP,
				localAddr:  ipv4String(row.dwLocalAddr),
				localPort:  ntohs(row.dwLocalPort),
				remoteAddr: ipv4String(row.dwRemoteAddr),
				remotePort: ntohs(row.dwRemotePort),
				state:      tcpStateToString(row.dwState),
				pid:        int32(row.dwOwningPid),
			})
			offset += rowSize
		}
	case afINET6:
		const rowSize = unsafe.Sizeof(mibTCP6RowOwnerPid{})
		offset := unsafe.Sizeof(uint32(0))
		for i := uint32(0); i < numEntries; i++ {
			row := (*mibTCP6RowOwnerPid)(unsafe.Pointer(&buf[offset]))
			result = append(result, rawConn{
				protocol:   ProtocolTCP,
				localAddr:  ipv6String(row.ucLocalAddr),
				localPort:  ntohs(row.dwLocalPort),
				remoteAddr: ipv6String(row.ucRemoteAddr),
				remotePort: ntohs(row.dwRemotePort),
				state:      tcpStateToString(row.dwState),
				pid:        int32(row.dwOwningPid),
			})
			offset += rowSize
		}
	}
	return result, nil
}

// getUDPTable 枚举指定族（v4/v6）的 UDP 端点表。
// UDP 行无 state/remote 字段，state 留空串（由 scanner.go 兜底成 "NONE"）。
func getUDPTable(family uint32) ([]rawConn, error) {
	buf, err := queryExtendedTable(family, false) // false=UDP
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, nil
	}

	numEntries := *(*uint32)(unsafe.Pointer(&buf[0]))
	if numEntries == 0 {
		return nil, nil
	}

	result := make([]rawConn, 0, numEntries)
	switch family {
	case afINET:
		const rowSize = unsafe.Sizeof(mibUDPRowOwnerPid{})
		offset := unsafe.Sizeof(uint32(0))
		for i := uint32(0); i < numEntries; i++ {
			row := (*mibUDPRowOwnerPid)(unsafe.Pointer(&buf[offset]))
			result = append(result, rawConn{
				protocol:  ProtocolUDP,
				localAddr: ipv4String(row.dwLocalAddr),
				localPort: ntohs(row.dwLocalPort),
				pid:       int32(row.dwOwningPid),
				// state/remote 留空：UDP 无连接状态，scanner.go 会兜底成 "NONE"
			})
			offset += rowSize
		}
	case afINET6:
		const rowSize = unsafe.Sizeof(mibUDP6RowOwnerPid{})
		offset := unsafe.Sizeof(uint32(0))
		for i := uint32(0); i < numEntries; i++ {
			row := (*mibUDP6RowOwnerPid)(unsafe.Pointer(&buf[offset]))
			result = append(result, rawConn{
				protocol:  ProtocolUDP,
				localAddr: ipv6String(row.ucLocalAddr),
				localPort: ntohs(row.dwLocalPort),
				pid:       int32(row.dwOwningPid),
			})
			offset += rowSize
		}
	}
	return result, nil
}

// queryExtendedTable 调用 GetExtendedTcpTable/GetExtendedUdpTable，返回填充好的字节缓冲。
// 两段式调用：先传 nil 查大小，再分配缓冲二次调用。
func queryExtendedTable(family uint32, tcp bool) ([]byte, error) {
	var size uint32
	// 第一次调用拿所需大小（返回 ERROR_INSUFFICIENT_BUFFER 是预期）
	call := func(buf []byte) (uint32, error) {
		var p uintptr
		if len(buf) > 0 {
			p = uintptr(unsafe.Pointer(&buf[0]))
		}
		var ret uintptr
		var err error
		if tcp {
			ret, _, err = procGetExtendedTcpTable.Call(
				p, uintptr(unsafe.Pointer(&size)), 1,
				uintptr(family), uintptr(tcpTableOwnerPidAll), 0,
			)
		} else {
			ret, _, err = procGetExtendedUdpTable.Call(
				p, uintptr(unsafe.Pointer(&size)), 1,
				uintptr(family), uintptr(udpTableOwnerPid), 0,
			)
		}
		_ = err
		return uint32(ret), nil
	}

	// 第一次：拿 size
	if code, _ := call(nil); code != 122 { // 122 = ERROR_INSUFFICIENT_BUFFER
		// size 为 0 表示无数据；其他非零错误返回
		if code != 0 {
			return nil, fmt.Errorf("GetExtendedTable 第一次调用失败，错误码 %d", code)
		}
		return nil, nil
	}

	buf := make([]byte, size)
	if code, _ := call(buf); code != 0 { // 0 = NO_ERROR
		return nil, fmt.Errorf("GetExtendedTable 第二次调用失败，错误码 %d", code)
	}
	return buf, nil
}

// ipv4String 把网络字节序（小端存储）的 IPv4 地址转成点分十进制字符串。
// 照 gopsutil parseIPv4HexString：addr 在内存里是 a.b.c.d 顺序存储。
func ipv4String(addr uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d",
		addr&0xFF,
		(addr>>8)&0xFF,
		(addr>>16)&0xFF,
		(addr>>24)&0xFF,
	)
}

// ipv6String 把 16 字节 IPv6 地址转成标准字符串（用 net.IP 标准化）。
func ipv6String(addr [16]byte) string {
	return net.IP(addr[:]).String()
}

// ntohs 反转网络字节序端口（照 gopsutil decodePort = syscall.Ntohs）。
func ntohs(port uint32) uint16 {
	return syscall.Ntohs(uint16(port))
}
