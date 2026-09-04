package zjmf

import (
	"encoding/json"
	"testing"
)

// tsl 上游 pid=1713 的真实响应样本（云电脑配置项组）
const tslReal = `{"data":{"config_groups":[{"name":"云电脑A型配置项组","options":[
 {"id":145348,"option_type":12,"option_name":"area|区域","sub":[
   {"option_name":"9|CN^陕西西安电信","pricings":[{"monthly":"0.00","quarterly":"0.00","annually":"0.00"}]},
   {"option_name":"10|CN^陕西西安联通","pricings":[{"monthly":"5.00","quarterly":"15.00","annually":"50.00"}]}]},
 {"id":145349,"option_type":5,"option_name":"os|操作系统","sub":[
   {"option_name":"30|Windows^Windows7_enterprise-cn","pricings":[{"monthly":"0.00"}]},
   {"option_name":"31|Linux^Ubuntu-22.04","pricings":[{"monthly":"0.00"}]}]},
 {"id":145350,"option_type":7,"option_name":"cpu|CPU","qty_minimum":2,"qty_maximum":16,"qty_stage":1,
  "sub":[{"option_name":"cpu","pricings":[{"monthly":10}]}]},
 {"id":145353,"option_type":1,"option_name":"nat_acl_limit|NAT转发","sub":[
   {"option_name":"5|免费5个","pricings":[{"monthly":"0.00"}]},
   {"option_name":"10|增加到10个","pricings":[{"monthly":"2.00"}]}]}
]}]}}`

func TestConvertTslReal(t *testing.T) {
	var raw zjmfConfigResp
	if err := json.Unmarshal([]byte(tslReal), &raw); err != nil {
		t.Fatal(err)
	}
	var opts []map[string]any
	for _, g := range raw.Data.ConfigGroups {
		opts = append(opts, g.Options...)
	}
	got := convertConfigOptions(opts)
	if len(got) != 4 {
		t.Fatalf("期望 4 个配置项，得到 %d", len(got))
	}
	area := got[0]
	if area.Field != "area" || area.Name != "区域" {
		t.Fatalf("area 字段错误: %+v", area)
	}
	if area.Subs[0].Name != "陕西西安电信" || area.Subs[0].Value != "9" {
		t.Fatalf("^ 分层拆分错误: %+v", area.Subs[0])
	}
	if area.Subs[1].Pricing["monthly"] != 5.0 || area.Subs[1].Pricing["yearly"] != 50.0 {
		t.Fatalf("联通周期加价解析错误: %+v", area.Subs[1])
	}
	osOpt := got[1]
	if osOpt.Field != "os" || osOpt.Subs[0].Name != "Windows7_enterprise-cn" && osOpt.Subs[0].Name != "Windows" {
		t.Logf("os 显示名: %q", osOpt.Subs[0].Name) // ^ 取末段
	}
	nat := got[3]
	if nat.Name != "NAT转发" {
		t.Fatalf("nat 名错误: %+v", nat)
	}
	if nat.Subs[1].Name != "增加到10个" || nat.Subs[1].Pricing["monthly"] != 2.0 {
		t.Fatalf("NAT 加价解析错误: %+v", nat.Subs[1])
	}
}

// tslConfigOpts 云电脑 get_product_config 样本（含 option id 与 sub id），对齐实测 145354/817206/817207。
const tslConfigOpts = `{"data":{"config_groups":[{"name":"g","options":[
 {"id":145348,"option_type":12,"option_name":"area|区域","sub":[
   {"id":817194,"option_name":"9|CN^陕西西安电信"}]},
 {"id":145349,"option_type":5,"option_name":"os|操作系统","sub":[
   {"id":817195,"option_name":"30|Windows^Windows7_enterprise-cn"},
   {"id":817196,"option_name":"92|Windows^Windows2008-多界面版本"}]},
 {"id":145350,"option_type":7,"option_name":"cpu|CPU","qty_minimum":2,"qty_maximum":16,"qty_stage":1,
  "sub":[{"id":900001,"option_name":"cpu"}]},
 {"id":145354,"option_type":1,"option_name":"nat_acl_limit|NAT转发","sub":[
   {"id":817206,"option_name":"5|免费5个"},
   {"id":817207,"option_name":"10|增加到10个"}]}
]}]}}`

// TestParseConfigOptions 目录回填顺带解析配置项（复用 get_product_config 响应，不重复请求）的入口。
func TestParseConfigOptions(t *testing.T) {
	got := parseConfigOptions([]byte(tslReal))
	if len(got) != 4 {
		t.Fatalf("期望 4 个配置项，得到 %d", len(got))
	}
	if got[0].Field != "area" || got[0].Name != "区域" {
		t.Fatalf("area 解析错误: %+v", got[0])
	}
	if got[2].Mode != "range" || got[2].Min != 2 || got[2].Max != 16 {
		t.Fatalf("CPU 数量档解析错误: %+v", got[2])
	}
	// 非 JSON 输入返回空（不 panic）
	if n := len(parseConfigOptions([]byte("not-json"))); n != 0 {
		t.Fatalf("非法输入应返回空，得到 %d", n)
	}
}

// TestConfigOptionMap 验证订单选择 field→子项值 被解析为上游 configoption[选项id]=子项id。
func TestConfigOptionMap(t *testing.T) {
	var pc map[string]any
	if err := json.Unmarshal([]byte(tslConfigOpts), &pc); err != nil {
		t.Fatal(err)
	}
	sel := map[string]string{
		"area": "9", "os": "92", "cpu": "8", "nat_acl_limit": "10",
	}
	got := configOptionMap(pc, sel)
	want := map[string]string{
		"145348": "817194", // 单选取子项 id
		"145349": "817196", // os 选 Windows2008
		"145350": "8",      // 数量型取所选数值
		"145354": "817207", // NAT 增加到10个 → 关键回归
	}
	if len(got) != len(want) {
		t.Fatalf("项数错误: got=%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("configoption[%s] 期望=%s 实际=%s (全部=%v)", k, v, got[k], got)
		}
	}
}
