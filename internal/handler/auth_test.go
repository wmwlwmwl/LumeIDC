package handler

import "testing"

// TestForgotChannel 找回密码账号渠道判定：含 @ 走邮箱，否则手机号；格式非法报错。
func TestForgotChannel(t *testing.T) {
	h := &Auth{}
	cases := []struct {
		name        string
		account     string
		channel     string
		dest        string
		scene       string
		wantErr     bool
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