package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/vsdk"
)

// 实名类型与注册表已迁 internal/vsdk（适配器可独立演进，见 internal/plugins/verify/）。
// 本文件为别名/转发平滑层：identity.go 等存量调用零改动。

type (
	VerificationProvider     = vsdk.Provider
	VerificationStartRequest = vsdk.StartRequest
	VerificationStartResult  = vsdk.StartResult
	VerificationStatus       = vsdk.Status
)

// ConfiguredVerificationProvider 根据后台选择的 provider 创建受信任的内置适配器。
// 薄包装：内部持 vsdk.Host（设置读取 + HTTP 工具）。
type ConfiguredVerificationProvider struct {
	host *vsdk.Host
}

// NewConfiguredVerificationProvider baseURL 参数保留仅为兼容调用签名（实名回调地址走
// VerificationStartRequest.ReturnURL，宿主不再需要）。
func NewConfiguredVerificationProvider(settings *repo.Settings, _ string) *ConfiguredVerificationProvider {
	return &ConfiguredVerificationProvider{host: &vsdk.Host{Settings: settings, Client: &http.Client{Timeout: 15 * time.Second}}}
}

// Provider 统一走 vsdk 注册表（适配器在 internal/plugins/verify/ init 自注册）。
func (f *ConfiguredVerificationProvider) Provider(_ context.Context, key string) (VerificationProvider, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	p, found, err := vsdk.New(key, f.host)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("实名服务适配器未注册：" + key)
	}
	return p, nil
}
