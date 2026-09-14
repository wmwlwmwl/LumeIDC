package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSMTPServer 极简 SMTP 服务：支持 EHLO/AUTH LOGIN/MAIL/RCPT/DATA/QUIT，
// 用于验证 smtpDeliver 的认证、投递与失败路径（不依赖数据库）。
type fakeSMTPServer struct {
	ln         net.Listener
	authMechs  string // 如 "LOGIN PLAIN"
	rejectAuth bool
	gotTo      string
	mu         sync.Mutex
	done       chan struct{}
}

func startFakeSMTP(t *testing.T, mechs string, rejectAuth bool) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	f := &fakeSMTPServer{ln: ln, authMechs: mechs, rejectAuth: rejectAuth, done: make(chan struct{})}
	go f.acceptLoop()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeSMTPServer) addr() string { return f.ln.Addr().String() }

func (f *fakeSMTPServer) acceptLoop() {
	defer close(f.done)
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeSMTPServer) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	send := func(s string) { _, _ = io.WriteString(c, s) }
	send("220 fake ESMTP ready\r\n")

	awaitUser, awaitPass := false, false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))

		switch {
		case strings.HasPrefix(cmd, "EHLO") || strings.HasPrefix(cmd, "HELO"):
			send("250-fake.local\r\n250 AUTH " + f.authMechs + "\r\n")
		case cmd == "QUIT":
			send("221 bye\r\n")
			return
		case awaitPass:
			awaitPass = false
			if f.rejectAuth {
				send("535 5.7.8 Authentication credentials invalid\r\n")
			} else {
				send("235 2.7.0 Authentication successful\r\n")
			}
		case awaitUser:
			awaitUser = false
			awaitPass = true
			send("334 UGFzc3dvcmQ6\r\n") // "Password:"
		case cmd == "AUTH LOGIN":
			awaitUser = true
			send("334 VXNlcm5hbWU6\r\n") // "Username:"
		case strings.HasPrefix(cmd, "AUTH "):
			send("503 5.5.4 auth not supported\r\n")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			send("250 2.1.0 Ok\r\n")
		case strings.HasPrefix(cmd, "RCPT TO"):
			// 简单提取收件人，供断言
			if i := strings.Index(strings.ToUpper(line), "TO:<"); i >= 0 {
				rest := line[i+4:]
				if j := strings.Index(rest, ">"); j >= 0 {
					f.mu.Lock()
					f.gotTo = rest[:j]
					f.mu.Unlock()
				}
			}
			send("250 2.1.5 Ok\r\n")
		case cmd == "DATA":
			send("354 End data with <CR><LF>.<CR><LF>\r\n")
			// 读取正文直到单独一行 "."
			for {
				d, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimSpace(d) == "." {
					break
				}
			}
			send("250 2.0.0 Ok: queued\r\n")
		default:
			// 忽略其它内容行
			send("250 Ok\r\n")
		}
	}
}

// startStalledSMTP 接受连接但不发 greeting，取消必须真正关闭连接才能让服务端退出。
func startStalledSMTP(t *testing.T) (string, int, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var active, peak atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n := active.Add(1)
			for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
			}
			go func() {
				defer c.Close()
				defer active.Add(-1)
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	host, portText, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	_, _ = fmt.Sscanf(portText, "%d", &port)
	return host, port, &active, &peak
}

func waitMail(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("等待邮件测试条件超时")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSMTPBoundedAndCanceled(t *testing.T) {
	host, port, active, peak := startStalledSMTP(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 20)
	for i := 0; i < cap(results); i++ {
		go func() { results <- smtpDeliver(ctx, host, port, "", "", "from@x.test", "to@x.test", nil) }()
	}
	waitMail(t, func() bool { return active.Load() == mailConnections })
	time.Sleep(30 * time.Millisecond)
	cancel()
	for i := 0; i < cap(results); i++ {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("取消错误丢失：%v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("取消未终止实际 SMTP 会话或额度等待")
		}
	}
	waitMail(t, func() bool { return active.Load() == 0 })
	if peak.Load() != mailConnections || len(smtpSlots) != 0 {
		t.Fatalf("连接峰值或额度泄漏：%d/%d", peak.Load(), len(smtpSlots))
	}
}

func TestMailCancellationDoesNotCooldown(t *testing.T) {
	host, port, active, _ := startStalledSMTP(t)
	n := &Notifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- n.sendWithAccounts(ctx, []MailAccount{{Enabled: true, Host: host, Port: port, From: "from@x.test"}}, "to@x.test", "测试", "正文")
	}()
	waitMail(t, func() bool { return active.Load() == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(n.cooldownUntil) != 0 {
		t.Fatal("取消请求不应令账号冷却")
	}
}

func TestSMTPDeadlineAndHeaderValidation(t *testing.T) {
	host, port, active, _ := startStalledSMTP(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := smtpDeliver(ctx, host, port, "", "", "from@x.test", "to@x.test", nil); err == nil {
		t.Fatal("超时会话未失败")
	}
	waitMail(t, func() bool { return active.Load() == 0 })
	n := &Notifier{}
	accounts := []MailAccount{{Host: host, Port: port, From: "from@x.test", Enabled: true}}
	for _, input := range []struct{ to, subject string }{{"to@x.test\r\nBcc: other@x.test", "测试"}, {"to@x.test", "测试\r\nBcc: other@x.test"}} {
		if err := n.sendAccount(context.Background(), accounts, 0, input.to, input.subject, "正文", false); err == nil {
			t.Fatal("未拒绝邮件头注入")
		}
	}
	if active.Load() != 0 {
		t.Fatal("无效邮件不应连接 SMTP")
	}
}

func TestMailAccountsFailoverAndCooldown(t *testing.T) {
	bad := startFakeSMTP(t, "LOGIN", true)
	good := startFakeSMTP(t, "LOGIN", false)
	accounts := make([]MailAccount, 2)
	for i, srv := range []*fakeSMTPServer{bad, good} {
		host, portText, _ := net.SplitHostPort(srv.addr())
		var port int
		_, _ = fmt.Sscanf(portText, "%d", &port)
		accounts[i] = MailAccount{Enabled: true, Host: host, Port: port, User: "用户", Pass: "密码", From: "from@x.test"}
	}
	n := &Notifier{}
	if err := n.sendWithAccounts(context.Background(), accounts, "to@x.test", "测试", "正文"); err != nil {
		t.Fatal(err)
	}
	if !n.cooldownUntil[0].After(time.Now()) || n.roundRobinCursor != 0 {
		t.Fatal("失败切换或轮询游标无效")
	}
	if err := n.sendWithAccounts(context.Background(), accounts[:1], "to@x.test", "测试", "正文"); err == nil || !strings.Contains(err.Error(), "冷却") {
		t.Fatal("单账号未遵循故障冷却")
	}
}

func TestMailRetryDelay(t *testing.T) {
	if mailRetryDelay(1) != 30*time.Second || mailRetryDelay(2) != time.Minute || mailRetryDelay(100) != time.Hour || mailLease <= mailSendTimeout+5*time.Second {
		t.Fatal("退避或租约常量无效")
	}
}

func TestMailHeaderFrom(t *testing.T) {
	if got := mailHeaderFrom("无名云", "admin@x.test"); !strings.HasSuffix(got, "?= <admin@x.test>") || strings.Contains(got, "无名云") {
		t.Fatalf("中文站点名未按 RFC 2047 编码: %q", got)
	}
	if got := mailHeaderFrom("LumeIDC", "admin@x.test"); got != `"LumeIDC" <admin@x.test>` {
		t.Fatalf("纯 ASCII 站点名应原样作显示名（加引号）: %q", got)
	}
	if got := mailHeaderFrom("  ", "admin@x.test"); got != "admin@x.test" {
		t.Fatalf("站点名为空时应只留地址: %q", got)
	}
}

func TestSmtpDeliverAuthLoginSuccess(t *testing.T) {
	srv := startFakeSMTP(t, "LOGIN", false)
	addr := srv.addr()
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	msg := buildMailMessage("from@x.test", "to@x.test", "hello", "body")

	if err := smtpDeliver(context.Background(), host, port, "user1", "pass1", "from@x.test", "to@x.test", msg); err != nil {
		t.Fatalf("投递失败: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.gotTo != "to@x.test" {
		t.Fatalf("未投递到目标收件人，got=%q", srv.gotTo)
	}
}

func TestSmtpDeliverAuthRejected(t *testing.T) {
	srv := startFakeSMTP(t, "LOGIN", true)
	host, portStr, _ := net.SplitHostPort(srv.addr())
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	msg := buildMailMessage("from@x.test", "to@x.test", "hello", "body")

	err := smtpDeliver(context.Background(), host, port, "user1", "wrong", "from@x.test", "to@x.test", msg)
	if err == nil {
		t.Fatal("期望认证失败但未返回错误")
	}
	if !strings.Contains(err.Error(), "用户名/密码") {
		t.Fatalf("错误未提示账号密码问题: %v", err)
	}
}

func TestSmtpDeliverConnectionRefusedFast(t *testing.T) {
	// 找一个必定空闲的端口：监听后立刻关闭
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	msg := buildMailMessage("from@x.test", "to@x.test", "hello", "body")

	start := time.Now()
	err = smtpDeliver(context.Background(), host, port, "", "", "from@x.test", "to@x.test", msg)
	if err == nil {
		t.Fatal("期望连接失败")
	}
	if !strings.Contains(err.Error(), "无法连接") {
		t.Fatalf("错误描述不清晰: %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("连接拒绝场景耗时异常，疑似阻塞")
	}
}
