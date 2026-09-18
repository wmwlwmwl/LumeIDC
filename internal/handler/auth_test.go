package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"lumeidc/internal/repo"
)

// TestForgotChannel 找回密码账号渠道判定：含 @ 走邮箱，否则手机号；格式非法报错。
func TestForgotChannel(t *testing.T) {
	h := &Auth{}
	cases := []struct {
		name    string
		account string
		channel string
		dest    string
		scene   string
		wantErr bool
	}{
		{name: "标准邮箱", account: "user@example.com", channel: "email", dest: "user@example.com", scene: "email_code"},
		{name: "邮箱转小写", account: "USER@Example.COM", channel: "email", dest: "user@example.com", scene: "email_code"},
		{name: "国内手机号", account: "13800138000", channel: "phone", dest: "+8613800138000", scene: "phone_code"},
		{name: "+86 手机号", account: "+86 138-0013-8000", channel: "phone", dest: "+8613800138000", scene: "phone_code"},
		{name: "非法手机号", account: "123", wantErr: true},
		{name: "非法邮箱", account: "a@", wantErr: true},
		{name: "空账号", account: "", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			channel, dest, scene, err := h.forgotChannel(c.account)
			if c.wantErr {
				if err == nil {
					t.Fatalf("forgotChannel(%q) 期望报错，got channel=%s dest=%s", c.account, channel, dest)
				}
				return
			}
			if err != nil {
				t.Fatalf("forgotChannel(%q) 意外报错: %v", c.account, err)
			}
			if channel != c.channel || dest != c.dest || scene != c.scene {
				t.Errorf("forgotChannel(%q) = (%s,%s,%s)，期望 (%s,%s,%s)", c.account, channel, dest, scene, c.channel, c.dest, c.scene)
			}
		})
	}
}

// 手机号密码登录必须能真正触发锁定：检查锁定、累计失败、成功清理三者必须用同一个键。
// 旧实现用原始输入（13800138000）检查锁定、却用归一化结果（+8613800138000）计数，
// 写入 login_attempts 的键永远查不到 → 锁定形同虚设，可无限撞库。
// 必须用不带 +86 的裸手机号登录才能复现，因此该用例也覆盖了按手机号登录的完整链路。
func TestLoginSubmitLocksOutByPhone(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_DATABASE_DSN，跳过")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	// 注意顺序：t.Cleanup 后进先出，这里先注册关闭，才能保证数据清理跑在连接关闭之前。
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	users := repo.NewUsers(d)

	const (
		barePhone  = "13800138000"    // 用户输入：裸号
		normalized = "+8613800138000" // 归一化后的账号
	)
	// 先清掉上一轮可能残留的数据（上次异常退出未走 cleanup），保证用例可重复运行。
	if _, err := d.ExecContext(ctx, `DELETE FROM users WHERE phone_e164=$1`, normalized); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM login_attempts WHERE key=$1 OR key=$2`, barePhone, normalized); err != nil {
		t.Fatal(err)
	}
	uid, err := users.CreateAccount(ctx, "", normalized, "correct-password-1", "锁定测试", true)
	if err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, uid); err != nil {
			t.Errorf("清理用户失败: %v", err)
		}
		if _, err := d.ExecContext(ctx, `DELETE FROM login_attempts WHERE key=$1 OR key=$2`, barePhone, normalized); err != nil {
			t.Errorf("清理登录计数失败: %v", err)
		}
	})

	h := &Auth{Users: users, Lockout: repo.NewLoginAttempts(d)}

	// 机制自检：锁定计数必须真的能落库。若这里就报错，说明是锁定存储本身坏了，
	// 而不是下面的「键不一致」问题——两者现象相同（永远 401），必须先区分开。
	selfKey := normalized + "#selfcheck"
	if _, err := d.ExecContext(ctx, `DELETE FROM login_attempts WHERE key=$1`, selfKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := d.ExecContext(context.Background(), `DELETE FROM login_attempts WHERE key=$1`, selfKey); err != nil {
			t.Errorf("清理自检计数失败: %v", err)
		}
	})
	if err := h.Lockout.Fail(ctx, selfKey); err != nil {
		t.Fatalf("锁定计数写入失败（锁定功能实际不可用）: %v", err)
	}
	if locked, err := h.Lockout.Locked(ctx, selfKey); err != nil || locked {
		t.Fatalf("仅 1 次失败不应进入锁定: locked=%v err=%v", locked, err)
	}

	submitWrongPassword := func() (int, string) {
		t.Helper()
		body := fmt.Sprintf(`{"email":%q,"password":"definitely-wrong"}`, barePhone)
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		h.loginSubmit(w, r)
		return w.Code, strings.TrimSpace(w.Body.String())
	}

	// 阈值 5 次：前 5 次是密码错误，第 6 次必须已被锁定。
	for i := 1; i <= 5; i++ {
		code, body := submitWrongPassword()
		if code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败登录应返回 401，实得 %d，响应: %s", i, code, body)
		}
		if !strings.Contains(body, "邮箱、手机号或密码错误") {
			t.Fatalf("第 %d 次应是密码错误而不是被其它校验拦下，响应: %s", i, body)
		}
	}
	if code, body := submitWrongPassword(); code != http.StatusTooManyRequests {
		t.Fatalf("连续失败 5 次后必须锁定（429），实得 %d，响应: %s", code, body)
	}
}
