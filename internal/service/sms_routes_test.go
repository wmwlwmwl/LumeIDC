package service

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSMSRouteSaveKeepsRangesIndependent(t *testing.T) {
	d := mailTestDB(t)
	n, _ := mailTestNotifier(t, d)
	ctx := context.Background()

	if err := n.SaveSMSSettings(ctx, map[string]string{
		"sms_provider": "stay33", "sms_username": "legacy", "sms_secret_key": "legacy-secret", "sms_sign_name": "旧国内签名",
	}); err != nil {
		t.Fatal(err)
	}
	if err := n.SaveSMSSettings(ctx, map[string]string{
		"sms_routes": `{"cn":{"provider":"stay33","sms_username":"","sms_secret_key":"","sms_sign_name":"新国内签名"},"global":{"provider":"smsbao","sms_username":"global-user","sms_secret_key":"global-secret","sms_sign_name":"国际签名"}}`,
	}); err != nil {
		t.Fatal(err)
	}
	cn, err := n.loadSMSRoute(ctx, d, SMSRangeCN)
	if err != nil || cn.Provider != "stay33" || cn.Config["sms_username"] != "legacy" || cn.Config["sms_secret_key"] != "legacy-secret" || cn.Config["sms_sign_name"] != "新国内签名" {
		t.Fatal("旧国内配置未正确迁移或保留", err, cn)
	}
	global, err := n.loadSMSRoute(ctx, d, SMSRangeGlobal)
	if err != nil || global.Provider != "smsbao" || global.Config["sms_username"] != "global-user" || global.Config["sms_secret_key"] != "global-secret" {
		t.Fatal("国际路由未独立保存", err, global)
	}
	oldGlobalFingerprint := global.Fingerprint
	if err := n.SaveSMSSettings(ctx, map[string]string{
		"sms_routes": `{"cn":{"provider":"stay33","sms_username":"","sms_secret_key":"","sms_sign_name":"再次修改国内"}}`,
	}); err != nil {
		t.Fatal(err)
	}
	global, err = n.loadSMSRoute(ctx, d, SMSRangeGlobal)
	if err != nil || global.Fingerprint != oldGlobalFingerprint {
		t.Fatal("更新国内路由影响了国际路由", err, global)
	}
	if _, err := n.loadSMSRoute(ctx, d, SMSRangeMarketing); err == nil {
		t.Fatal("未配置营销路由仍被当作可用")
	}
	public, err := n.PublicSMSRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var visible map[string]map[string]string
	if err := json.Unmarshal([]byte(public), &visible); err != nil {
		t.Fatal(err)
	}
	if visible["cn"]["sms_secret_key"] != "" || visible["global"]["sms_secret_key"] != "" {
		t.Fatal("路由密钥被返回到管理接口")
	}
}

func TestSMSRouteRejectsIncompatibleProvider(t *testing.T) {
	d := mailTestDB(t)
	n, _ := mailTestNotifier(t, d)
	ctx := context.Background()
	for _, raw := range []string{
		`{"global":{"provider":"stay33","sms_username":"u","sms_secret_key":"s"}}`,
		`{"marketing":{"provider":"idcsmart","sms_username":"u","sms_secret_key":"s"}}`,
		`{"cn":{"provider":"unknown","sms_secret_key":"s"}}`,
	} {
		if err := n.SaveSMSSettings(ctx, map[string]string{"sms_routes": raw}); err == nil {
			t.Fatal("不兼容路由被接受", raw)
		}
	}
}
