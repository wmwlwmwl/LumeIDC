package service

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// stay33VerificationAdapter Stay33 实名适配器。
// 与原 stay33VerificationProvider 行为完全一致，通过 init() 注册到工厂。
type stay33VerificationAdapter struct {
	factory *ConfiguredVerificationProvider
}

func newStay33VerificationAdapter(f *ConfiguredVerificationProvider) (VerificationProvider, error) {
	return &stay33VerificationAdapter{factory: f}, nil
}

func (p *stay33VerificationAdapter) Key() string { return "stay33" }

func (p *stay33VerificationAdapter) endpoint(ctx context.Context) (string, error) {
	return p.factory.endpoint(ctx, "verification_stay33_api_url", "https://idc.stay33.cn/realname/certapi.php", "idc.stay33.cn")
}

func (p *stay33VerificationAdapter) headers(ctx context.Context) (map[string]string, error) {
	apiKey, secret := p.factory.get(ctx, "verification_stay33_api_key"), p.factory.get(ctx, "verification_stay33_secret_key")
	if apiKey == "" || secret == "" {
		return nil, errors.New("Stay33 实名配置不完整")
	}
	return map[string]string{"api": apiKey, "key": secret}, nil
}

func (p *stay33VerificationAdapter) Start(ctx context.Context, in VerificationStartRequest) (VerificationStartResult, error) {
	endpoint, err := p.endpoint(ctx)
	if err != nil {
		return VerificationStartResult{}, err
	}
	headers, err := p.headers(ctx)
	if err != nil {
		return VerificationStartResult{}, err
	}
	nonce := providerRandomHex(8)
	values := url.Values{"action": {"initialize"}, "outer_order_no": {"ZGYD" + nonce}, "biz_code": {p.factory.get(ctx, "verification_stay33_biz_code")}, "cert_type": {"0"}, "cert_name": {in.LegalName}, "cert_no": {in.IdentityNumber}, "return_url": {in.ReturnURL}}
	var result map[string]any
	if err := p.factory.requestForm(ctx, endpoint, values, headers, &result); err != nil {
		return VerificationStartResult{}, err
	}
	if !responseOK(result) {
		return VerificationStartResult{}, errors.New("Stay33 实名初始化失败")
	}
	ref := firstString(result, "certify_id", "id")
	if data, ok := result["data"].(map[string]any); ok && ref == "" {
		ref = firstString(data, "certify_id", "id")
	}
	if ref == "" {
		return VerificationStartResult{}, errors.New("Stay33 未返回认证编号")
	}
	certValues := url.Values{"action": {"certify"}, "certify_id": {ref}}
	var cert map[string]any
	if err := p.factory.requestForm(ctx, endpoint, certValues, headers, &cert); err != nil {
		return VerificationStartResult{}, err
	}
	verifyURL := firstString(cert, "url", "verify_url")
	if data, ok := cert["data"].(map[string]any); ok && verifyURL == "" {
		verifyURL = firstString(data, "url", "verify_url")
	}
	return VerificationStartResult{ProviderRef: ref, URL: verifyURL}, nil
}

func (p *stay33VerificationAdapter) Poll(ctx context.Context, ref string) (VerificationStatus, error) {
	endpoint, err := p.endpoint(ctx)
	if err != nil {
		return VerificationStatus{}, err
	}
	headers, err := p.headers(ctx)
	if err != nil {
		return VerificationStatus{}, err
	}
	values := url.Values{"action": {"query"}, "certify_id": {ref}}
	var result map[string]any
	if err := p.factory.requestForm(ctx, endpoint, values, headers, &result); err != nil {
		return VerificationStatus{}, err
	}
	if !responseOK(result) {
		return VerificationStatus{Status: "rejected", Message: firstString(result, "msg", "message")}, nil
	}
	status := strings.ToLower(firstString(result, "status", "msg", "message"))
	if status == "1" || status == "success" || status == "passed" {
		return VerificationStatus{Status: "approved"}, nil
	}
	if strings.Contains(status, "等待") || strings.Contains(status, "处理中") || strings.Contains(status, "审核中") || status == "pending" {
		return VerificationStatus{Status: "pending"}, nil
	}
	return VerificationStatus{Status: "rejected", Message: status}, nil
}

func init() {
	registerVerificationAdapter("stay33", newStay33VerificationAdapter)
}
