package service

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// MailSender 邮件出站渠道（接口类插件注册点，与短信 registerSMSProvider 同模式）。
// SMTP 是内置默认渠道（settings.mail_channel 为空或 "smtp"）；
// 插件可提供 HTTP API 类渠道（SendGrid、阿里云邮件推送等），后台邮件设置页下拉切换。
type MailSender interface {
	Name() string // 渠道标识（settings.mail_channel 存储值，小写蛇形）
	Label() string
	Send(ctx context.Context, to, subject, body, format string) error
}

var (
	mailSenderMu sync.RWMutex
	mailSenders  = map[string]MailSender{}
)

// RegisterMailSender 注册邮件渠道（插件包 init 调用；重名 panic，与 plugin.Register 同策略）。
func RegisterMailSender(s MailSender) {
	name := s.Name()
	if name == "" {
		panic("service: 邮件渠道缺少 Name")
	}
	mailSenderMu.Lock()
	defer mailSenderMu.Unlock()
	if _, dup := mailSenders[name]; dup {
		panic(fmt.Sprintf("service: 邮件渠道重复注册: %s", name))
	}
	mailSenders[name] = s
}

// MailSenders 全部已注册渠道（按 Name 排序，后台渠道下拉用）。
func MailSenders() []MailSender {
	mailSenderMu.RLock()
	defer mailSenderMu.RUnlock()
	out := make([]MailSender, 0, len(mailSenders))
	for _, s := range mailSenders {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// mailSenderFor 按名取渠道。
func mailSenderFor(name string) (MailSender, bool) {
	mailSenderMu.RLock()
	defer mailSenderMu.RUnlock()
	s, ok := mailSenders[name]
	return s, ok
}
