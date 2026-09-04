//go:build !linux

package update

import "errors"

// ExecNew 就地重启仅支持 Linux；其它平台禁用。
func (c *Client) ExecNew() error {
	return errors.New("就地重启仅支持 Linux 服务器")
}
