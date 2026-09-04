//go:build linux

package update

import (
	"os"
	"syscall"
)

// ExecNew 用当前（已替换的）二进制就地重启进程：execve 后 PID 不变，
// 新进程重新加载配置并接管监听端口，等价于一次服务重启。
func (c *Client) ExecNew() error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(path, os.Args, os.Environ())
}
