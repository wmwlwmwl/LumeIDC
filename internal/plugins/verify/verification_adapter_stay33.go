package verify

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"lumeidc/internal/vsdk"
)

// stay33VerificationAdapter Stay33 实名适配器。
type stay33VerificationAdapter struct {
	host *vsdk.Host
}

func newStay33VerificationAdapter(h *vsdk.Host) (vsdk.Provider, error) {
	return &stay33VerificationAdapter{host: h}, nil
}

func (p *stay33VerificationAdapter) Key() string { return "stay33" }

func (p *stay33VerificationAdapter) endpoint(ctx context.Context) (string, error) {
	return p.host.Endpoint(ctx, "verification_stay33_api_url", "https://idc.stay33.cn/realname/certapi.php", "idc.stay33.cn")
}

func (p *stay33VerificationAdapter) headers(ctx context.Context) (map[string]string, error) {
	apiKey, secret := p.host.Get(ctx, "verification_stay33_api_key"), p.host.Get(ctx, "verification_stay33_secret_key")
	if apiKey == "" || secret == "" {
		return nil, errors.New("Stay33 实名配置不完整")
	}
	return map[string]string{"api": apiKey, "key": secret}, nil
}

func (p *stay33VerificationAdapter) Start(ctx context.Context, in vsdk.StartRequest) (vsdk.StartResult, error) {
	endpoint, err := p.endpoint(ctx)
	if err != nil {
		return vsdk.StartResult{}, err
	}
	headers, err := p.headers(ctx)
	if err != nil {
		return vsdk.StartResult{}, err
	}
	nonce := vsdk.RandomHex(8)
	values := url.Values{"action": {"initialize"}, "outer_order_no": {"ZGYD" + nonce}, "biz_code": {p.host.Get(ctx, "verification_stay33_biz_code")}, "cert_type": {"0"}, "cert_name": {in.LegalName}, "cert_no": {in.IdentityNumber}, "return_url": {in.ReturnURL}}
	var result map[string]any
	if err := p.host.RequestForm(ctx, endpoint, values, headers, &result); err != nil {
		return vsdk.StartResult{}, err
	}
	if !vsdk.ResponseOK(result) {
		return vsdk.StartResult{}, errors.New("Stay33 实名初始化失败")
	}
	ref := vsdk.FirstString(result, "certify_id", "id")
	if data, ok := result["data"].(map[string]any); ok && ref == "" {
		ref = vsdk.FirstString(data, "certify_id", "id")
	}
	if ref == "" {
		return vsdk.StartResult{}, errors.New("Stay33 未返回认证编号")
	}
	certValues := url.Values{"action": {"certify"}, "certify_id": {ref}}
	var cert map[string]any
	if err := p.host.RequestForm(ctx, endpoint, certValues, headers, &cert); err != nil {
		return vsdk.StartResult{}, err
	}
	verifyURL := vsdk.FirstString(cert, "url", "verify_url")
	if data, ok := cert["data"].(map[string]any); ok && verifyURL == "" {
		verifyURL = vsdk.FirstString(data, "url", "verify_url")
	}
	return vsdk.StartResult{ProviderRef: ref, URL: verifyURL}, nil
}

func (p *stay33VerificationAdapter) Poll(ctx context.Context, ref string) (vsdk.Status, error) {
	endpoint, err := p.endpoint(ctx)
	if err != nil {
		return vsdk.Status{}, err
	}
	headers, err := p.headers(ctx)
	if err != nil {
		return vsdk.Status{}, err
	}
	values := url.Values{"action": {"query"}, "certify_id": {ref}}
	var result map[string]any
	if err := p.host.RequestForm(ctx, endpoint, values, headers, &result); err != nil {
		return vsdk.Status{}, err
	}
	if !vsdk.ResponseOK(result) {
		return vsdk.Status{Status: "rejected", Message: vsdk.FirstString(result, "msg", "message")}, nil
	}
	status := strings.ToLower(vsdk.FirstString(result, "status", "msg", "message"))
	if status == "1" || status == "success" || status == "passed" {
		return vsdk.Status{Status: "approved"}, nil
	}
	if strings.Contains(status, "等待") || strings.Contains(status, "处理中") || strings.Contains(status, "审核中") || status == "pending" {
		return vsdk.Status{Status: "pending"}, nil
	}
	return vsdk.Status{Status: "rejected", Message: status}, nil
}

var descriptorStay33 = vsdk.Descriptor{
	Key:  "stay33",
	Name: "Stay33",
	Fields: []vsdk.ConfigField{
		{Key: "verification_stay33_api_key", Label: "API Key"},
		{Key: "verification_stay33_api_url", Label: "接口地址"},
		{Key: "verification_stay33_biz_code", Label: "业务编码"},
		{Key: "verification_stay33_secret_key", Label: "Secret Key", Secret: true},
	},
	ReturnHost: "idc.stay33.cn",
}

func init() {
	vsdk.Register(descriptorStay33, newStay33VerificationAdapter)
}
