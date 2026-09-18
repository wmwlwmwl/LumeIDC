package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

type SMSRoute struct {
	RangeType   SMSRange          `json:"range_type"`
	Provider    string            `json:"provider"`
	Config      map[string]string `json:"config"`
	Fingerprint string            `json:"fingerprint"`
}

type smsRoutesInput map[SMSRange]map[string]string

func validSMSRange(r SMSRange) bool {
	return r == SMSRangeCN || r == SMSRangeGlobal || r == SMSRangeMarketing
}

func smsRouteConfig(values map[string]string, r SMSRange) (string, map[string]string, error) {
	provider := strings.ToLower(strings.TrimSpace(values["provider"]))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(values["sms_provider"]))
	}
	d, ok := SMSProviderDescriptorFor(provider)
	if !ok || (!smsProviderSupports(provider, r, "notification") && !(r == SMSRangeCN && smsProviderSupports(provider, r, "otp"))) {
		return "", nil, errors.New("短信服务商不支持该发送范围")
	}
	allowed := map[string]bool{"provider": true, "sms_provider": true}
	for _, key := range d.ConfigFields {
		allowed[key] = true
	}
	config := map[string]string{"sms_provider": provider}
	for key, value := range values {
		value = strings.TrimSpace(value)
		// 旧扁平配置会携带其他服务商留下的空字段；空值不参与当前路由。
		if !allowed[key] {
			if value == "" {
				continue
			}
			return "", nil, errors.New("短信路由包含未知或不适用字段")
		}
		if !smsTextValid(value, 1000) {
			return "", nil, errors.New("短信设置含控制字符或过长")
		}
		if key != "provider" && key != "sms_provider" {
			config[key] = value
		}
	}
	return provider, config, nil
}

func parseSMSRoutes(raw string) (smsRoutesInput, error) {
	if strings.TrimSpace(raw) == "" {
		return smsRoutesInput{}, nil
	}
	var decoded map[string]map[string]string
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("短信路由 JSON 无效")
	}
	out := smsRoutesInput{}
	for key, route := range decoded {
		r := SMSRange(key)
		if !validSMSRange(r) || route == nil {
			return nil, errors.New("短信路由范围无效")
		}
		if _, _, err := smsRouteConfig(route, r); err != nil {
			return nil, err
		}
		out[r] = route
	}
	return out, nil
}

func smsRouteFingerprint(provider string, config map[string]string) string {
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	h.Write([]byte(provider))
	for _, key := range keys {
		h.Write([]byte{0})
		h.Write([]byte(key))
		h.Write([]byte{0})
		h.Write([]byte(config[key]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func smsRoutePublic(r SMSRoute) map[string]string {
	out := map[string]string{"provider": r.Provider}
	for key, value := range r.Config {
		if key != "sms_secret_key" && key != "sms_global_secret_key" && key != "sms_api_key" && key != "sms_token" {
			out[key] = value
		}
	}
	return out
}

func (n *Notifier) loadSMSRoute(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, r SMSRange) (SMSRoute, error) {
	if !validSMSRange(r) {
		return SMSRoute{}, errors.New("短信路由范围无效")
	}
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT coalesce((SELECT value FROM settings WHERE key='sms_routes'),'')`).Scan(&raw); err != nil {
		return SMSRoute{}, errors.New("读取短信路由失败")
	}
	routes, err := parseSMSRoutes(raw)
	if err != nil {
		return SMSRoute{}, err
	}
	input, ok := routes[r]
	if !ok {
		if r != SMSRangeCN {
			return SMSRoute{}, errors.New("该短信范围未配置路由")
		}
		input = map[string]string{}
		for _, key := range smsSettingKeys {
			var value string
			if err := db.QueryRowContext(ctx, `SELECT coalesce((SELECT value FROM settings WHERE key=$1),'')`, key).Scan(&value); err != nil {
				return SMSRoute{}, errors.New("读取短信配置失败")
			}
			input[key] = strings.TrimSpace(value)
		}
	}
	provider, config, err := smsRouteConfig(input, r)
	if err != nil {
		return SMSRoute{}, err
	}
	return SMSRoute{RangeType: r, Provider: provider, Config: config, Fingerprint: smsRouteFingerprint(provider, config)}, nil
}

func (n *Notifier) PublicSMSRoutes(ctx context.Context) (string, error) {
	out := map[string]map[string]string{}
	for _, r := range []SMSRange{SMSRangeCN, SMSRangeGlobal, SMSRangeMarketing} {
		route, err := n.loadSMSRoute(ctx, n.db, r)
		if err != nil {
			if r == SMSRangeCN {
				return "", err
			}
			continue
		}
		out[string(r)] = smsRoutePublic(route)
	}
	raw, _ := json.Marshal(out)
	return string(raw), nil
}

// smsLegacyRouteInput 把旧扁平配置收敛成单个路由可用的输入：只保留该服务商声明的字段。
// 旧配置里会同名存着历史别名（如 stay33 除 sms_secret_key 外另写一份 sms_api_key/sms_token，
// 见 SaveSMSSettings），这些字段对当前服务商未知，直接喂给 smsRouteConfig 会被判成
// 「包含未知或不适用字段」而整段迁移失败——迁移静默失败后此处留空的密钥再也补不回来。
func smsLegacyRouteInput(legacy map[string]string) map[string]string {
	provider := strings.ToLower(strings.TrimSpace(legacy["provider"]))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(legacy["sms_provider"]))
	}
	keep := map[string]bool{"provider": true, "sms_provider": true}
	if d, ok := SMSProviderDescriptorFor(provider); ok {
		for _, key := range d.ConfigFields {
			keep[key] = true
		}
	}
	out := map[string]string{}
	for key, value := range legacy {
		if keep[key] {
			out[key] = value
		}
	}
	return out
}

func (n *Notifier) saveSMSRoutes(ctx context.Context, tx *sql.Tx, routes smsRoutesInput) error {
	var oldRaw string
	if err := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT value FROM settings WHERE key='sms_routes'),'')`).Scan(&oldRaw); err != nil {
		return errors.New("读取短信路由失败")
	}
	oldRoutes, err := parseSMSRoutes(oldRaw)
	if err != nil {
		return err
	}
	// 第一次迁移到结构化路由时，国内沿用已保存的旧单通道凭据；不要求管理员重复录入密钥。
	if _, ok := oldRoutes[SMSRangeCN]; !ok {
		legacy := map[string]string{}
		for _, key := range smsSettingKeys {
			var value string
			if err := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT value FROM settings WHERE key=$1),'')`, key).Scan(&value); err != nil {
				return errors.New("读取旧短信配置失败")
			}
			legacy[key] = strings.TrimSpace(value)
		}
		if provider, config, routeErr := smsRouteConfig(smsLegacyRouteInput(legacy), SMSRangeCN); routeErr == nil && provider != "" {
			oldRoutes[SMSRangeCN] = map[string]string{"provider": provider}
			for key, value := range config {
				if key != "sms_provider" {
					oldRoutes[SMSRangeCN][key] = value
				}
			}
		}
	}
	stored := map[string]map[string]string{}
	for r, route := range oldRoutes {
		stored[string(r)] = map[string]string{}
		for key, value := range route {
			stored[string(r)][key] = value
		}
	}
	for r, input := range routes {
		provider, config, err := smsRouteConfig(input, r)
		if err != nil {
			return err
		}
		oldProvider, oldConfig, oldOK := "", map[string]string{}, false
		if oldInput, ok := oldRoutes[r]; ok {
			oldProvider, oldConfig, err = smsRouteConfig(oldInput, r)
			oldOK = err == nil
		}
		// 同一服务商下留空表示「保持不变」：前端保存后即清空密钥输入框，服务端必须据此沿用旧值。
		// 沿用范围不能只限密钥——stay33/短信宝 等要账号与密钥配套，只补密钥会把账号一起清掉，认证必失败。
		// ponytail: 代价是同服务商下无法靠清空字段重置某项配置（与前端「留空保持不变」一致）；
		// 若日后需要"显式清空"，改用请求里区分「未提交」与「提交为空」的显式标记。
		if oldOK && oldProvider == provider {
			for key, value := range oldConfig {
				if config[key] == "" {
					config[key] = value
				}
			}
		}
		if config["sms_secret_key"] == "" && config["sms_global_secret_key"] == "" {
			return errors.New("新建或切换短信服务商必须填写密钥")
		}
		stored[string(r)] = map[string]string{"provider": provider}
		for key, value := range config {
			if key != "sms_provider" {
				stored[string(r)][key] = value
			}
		}
	}
	raw, _ := json.Marshal(stored)
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('sms_routes',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw)); err != nil {
		return errors.New("保存短信路由失败")
	}
	return nil
}
