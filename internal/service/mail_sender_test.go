package service

import (
	"context"
	"testing"
)

type stubMailSender struct{ name, label string }

func (s stubMailSender) Name() string  { return s.name }
func (s stubMailSender) Label() string { return s.label }
func (s stubMailSender) Send(ctx context.Context, to, subject, body, format string) error {
	return nil
}

func TestMailSenderRegistry(t *testing.T) {
	RegisterMailSender(stubMailSender{name: "t_sendgrid", label: "SendGrid"})
	RegisterMailSender(stubMailSender{name: "t_alimail", label: "阿里云邮件推送"})

	s, ok := mailSenderFor("t_sendgrid")
	if !ok || s.Label() != "SendGrid" {
		t.Fatalf("按名取渠道失败: ok=%v", ok)
	}
	if _, ok := mailSenderFor("not_exists"); ok {
		t.Fatal("未注册渠道不应命中")
	}

	// 列表按 Name 排序
	all := MailSenders()
	var names []string
	for _, m := range all {
		names = append(names, m.Name())
	}
	if len(names) < 2 || names[0] != "t_alimail" || names[1] != "t_sendgrid" {
		t.Fatalf("渠道列表应排序: %v", names)
	}
	// 清理（避免污染其他用例）
	mailSenderMu.Lock()
	delete(mailSenders, "t_sendgrid")
	delete(mailSenders, "t_alimail")
	mailSenderMu.Unlock()
}

func TestRegisterMailSenderDupPanics(t *testing.T) {
	RegisterMailSender(stubMailSender{name: "t_dup", label: "x"})
	defer func() {
		if recover() == nil {
			t.Fatal("重名注册应 panic")
		}
		mailSenderMu.Lock()
		delete(mailSenders, "t_dup")
		mailSenderMu.Unlock()
	}()
	RegisterMailSender(stubMailSender{name: "t_dup", label: "y"})
}
