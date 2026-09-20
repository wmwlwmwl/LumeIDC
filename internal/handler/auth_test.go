package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"lumeidc/internal/config"
	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

// captureOTP 捕获验证码服务发出的短信验证码，避免测试真的发短信。
type captureOTP struct{ code *string }

func (c captureOTP) SendPurpose(_ context.Context, _, code, _ string) error {
	*c.code = code
	return nil
}

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

// 注册发码接口「账号已存在」分支必须计入限速。
// 该分支不发码、也不经过验证码服务（限速在 Issue 内），旧实现直接返回，
// 未认证者便能按响应文案无限次枚举出已注册的邮箱/手机号。
func TestRegisterCodeThrottlesExistingAccountProbe(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_DATABASE_DSN，跳过")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	users := repo.NewUsers(d)
	email := "regprobe-" + time.Now().Format("150405.000000000") + "@example.invalid"
	uid, err := users.CreateAccount(ctx, email, "", "correct-password-1", "探测测试", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, uid); err != nil {
			t.Errorf("清理用户失败: %v", err)
		}
		// 探测计数键形如 regprobe:<IP>，按前缀清理本用例写入的行
		if _, err := d.ExecContext(ctx, `DELETE FROM login_attempts WHERE key LIKE $1`, registerProbePrefix+"%"); err != nil {
			t.Errorf("清理注册探测计数失败: %v", err)
		}
	})

	// Challenges 只需非空：账号已存在的分支在调用验证码服务之前就返回了。
	h := &Auth{
		Users:      users,
		Lockout:    repo.NewLoginAttempts(d),
		Challenges: &service.AuthChallengeService{Store: repo.NewAuthChallenges(d), Key: []byte("unit-test-challenge-key-32-bytes!")},
	}
	probe := func() (int, string) {
		t.Helper()
		body := fmt.Sprintf(`{"mode":"email","email":%q}`, email)
		r := httptest.NewRequest(http.MethodPost, "/auth/register-code", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		h.registerCode(w, r)
		return w.Code, strings.TrimSpace(w.Body.String())
	}

	// 阈值 5 次：前 5 次正常告知「已注册」，第 6 次必须已按 IP 限速。
	for i := 1; i <= 5; i++ {
		code, resp := probe()
		if code != http.StatusBadRequest || !strings.Contains(resp, "已注册") {
			t.Fatalf("第 %d 次探测应返回 400 已注册，实得 %d，响应: %s", i, code, resp)
		}
	}
	if code, resp := probe(); code != http.StatusTooManyRequests {
		t.Fatalf("同类探测超过阈值必须限速（429），实得 %d，响应: %s", code, resp)
	}
}

// both 模式下手机号同样是「发码并校验通过」的，必须落 phone_verified_at：
// 漏写会让用户之后无法用手机号找回密码、也无法提交人工实名（两者都要求手机号已验证）。
func TestRegisterBothModeMarksPhoneVerified(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_DATABASE_DSN，跳过")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	users := repo.NewUsers(d)
	settings := repo.NewSettings(d)
	// 打开手机号注册（both 模式要求邮箱与手机号都启用）。共享测试库，用例结束必须复原。
	prev, prevErr := settings.Get(ctx, "registration_phone_enabled")
	if err := settings.Set(ctx, "registration_phone_enabled", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		restore := ""
		if prevErr == nil {
			restore = prev
		}
		if err := settings.Set(context.Background(), "registration_phone_enabled", restore); err != nil {
			t.Errorf("复原手机号注册开关失败: %v", err)
		}
	})

	var code string
	challenges := &service.AuthChallengeService{
		Store: repo.NewAuthChallenges(d),
		Key:   []byte("unit-test-challenge-key-32-bytes!"),
		SMS:   captureOTP{code: &code},
	}
	store, err := middleware.NewStore(&config.Config{
		SecretKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Auth{Users: users, Settings: settings, Challenges: challenges, Sessions: store}

	const phone = "+8613800138123"
	email := "bothmode-" + time.Now().Format("150405.000000000") + "@example.invalid"
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []string{
			`DELETE FROM auth_challenges WHERE request_ip='192.0.2.9'`,
			`DELETE FROM users WHERE phone_e164='` + phone + `'`,
		} {
			if _, err := d.ExecContext(ctx, q); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", q, err)
			}
		}
	})

	// 夹具前提：邮箱验证默认关闭、手机验证默认必需，故 both 模式只校验手机验证码。
	if err := challenges.Issue(ctx, "phone", "register", phone, "192.0.2.9"); err != nil {
		t.Fatalf("签发手机验证码失败: %v", err)
	}
	if code == "" {
		t.Fatal("夹具前提不成立：未捕获到手机验证码")
	}

	body := fmt.Sprintf(`{"mode":"both","email":%q,"phone":%q,"phone_code":%q,`+
		`"password":"correct-password-1","password_confirm":"correct-password-1"}`, email, phone, code)
	r := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.registerSubmit(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("both 模式注册应成功，实得 %d，响应: %s", w.Code, strings.TrimSpace(w.Body.String()))
	}

	var phoneVerified bool
	if err := d.QueryRowContext(ctx, `SELECT phone_verified_at IS NOT NULL FROM users WHERE phone_e164=$1`, phone).Scan(&phoneVerified); err != nil {
		t.Fatal(err)
	}
	if !phoneVerified {
		t.Fatal("both 模式注册后 phone_verified_at 必须已写入（手机号确实发码并校验通过）")
	}
}
