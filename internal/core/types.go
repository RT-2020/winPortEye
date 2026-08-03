// Package core 提供端口监控的核心能力：端口枚举、进程查询、进程终止。
// GUI 模式和 MCP 模式共用这一层，绝不重复实现。
package core

// Protocol 表示网络协议类型。
type Protocol string

const (
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"
)

// Connection 表示一条网络连接（TCP/UDP），含占用进程信息。
type Connection struct {
	Protocol    Protocol `json:"protocol"`    // tcp / udp
	LocalAddr   string   `json:"localAddr"`   // 本地地址（IPv4/IPv6 文本）
	LocalPort   uint16   `json:"localPort"`   // 本地端口
	RemoteAddr  string   `json:"remoteAddr"`  // 远端地址（UDP 通常为空）
	RemotePort  uint16   `json:"remotePort"`  // 远端端口（UDP 通常为 0）
	State       string   `json:"state"`       // LISTEN / ESTABLISHED / TIME_WAIT...（UDP 为 "NONE"）
	Pid         int32    `json:"pid"`         // 占用进程 PID
	ProcessName string   `json:"processName"` // 进程名（权限不足时可能为空）
	ProcessPath string   `json:"processPath"` // 进程可执行文件完整路径（权限不足时可能为空）
}

// ProcessInfo 表示一个进程的详细信息。
type ProcessInfo struct {
	Pid         int32  `json:"pid"`
	Name        string `json:"name"`        // 进程名
	Path        string `json:"path"`        // 可执行文件完整路径
	CommandLine string `json:"commandLine"` // 启动命令行
	CreateTime  int64  `json:"createTime"`  // 创建时间（毫秒，自 epoch）
}

// KillResult 表示一次杀进程操作的结果。
type KillResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"` // 失败原因：权限不足 / 受保护 / 进程不存在 ...
	Pid     int32  `json:"pid"`
}
