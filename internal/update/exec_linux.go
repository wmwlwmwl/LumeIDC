//go:build linux

package update

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// ExecNew 用当前（已替换的）二进制就地重启进程：execve 后 PID 不变，
// 新进程重新加载配置并接管监听端口，等价于一次服务重启。
//
// 注意不能直接依赖 os.Executable()（/proc/self/exe）：替换流程会先把运行中的
// 二进制 rename 成 xxx.bak 再放新文件，此时 /proc/self/exe 跟随 inode 解析出的
// 路径是 xxx.bak。直接 exec 它会“把旧版本又拉起来”，所以优先使用 Apply 时记录的
// 原始路径，并兜底剔除 .bak 后缀。
func (c *Client) ExecNew() error {
	path := c.execPath
	if path == "" {
		p, err := os.Executable()
		if err != nil {
			return err
		}
		path = p
	}
	path = strings.TrimSuffix(path, ".bak")
	// 预检：替换后 exec 失败只能继续跑旧进程（版本号不变），先拦截文件缺失/无执行权限。
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("新版本程序不存在（%v）：请检查更新文件是否被清理", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("新版本程序没有执行权限，请先 chmod +x %s", path)
	}
	return syscall.Exec(path, os.Args, os.Environ())
}