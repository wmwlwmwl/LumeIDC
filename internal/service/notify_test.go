package service

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
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
					f.gotTo = rest[:j]
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

func TestSmtpDeliverAuthLoginSuccess(t *testing.T) {
	srv := startFakeSMTP(t, "LOGIN", false)
	addr := srv.addr()
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	msg := buildMailMessage("from@x.test", "to@x.test", "hello", "body")

	if err := smtpDeliver(host, port, "user1", "pass1", "from@x.test", "to@x.test", msg); err != nil {
		t.Fatalf("投递失败: %v", err)
	}
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

	err := smtpDeliver(host, port, "user1", "wrong", "from@x.test", "to@x.test", msg)
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
	err = smtpDeliver(host, port, "", "", "from@x.test", "to@x.test", msg)
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
