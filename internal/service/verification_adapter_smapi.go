package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// smapiAdapter Smapi 实名适配器。
// 与原 smapiProvider 行为完全一致，通过 init() 注册到工厂。
type smapiAdapter struct {
	factory *ConfiguredVerificationProvider
}

func newSmapiAdapter(f *ConfiguredVerificationProvider) (VerificationProvider, error) {
	return &smapiAdapter{factory: f}, nil
}

func (p *smapiAdapter) Key() string { return "smapi" }

func (p *smapiAdapter) endpoint(ctx context.Context) (string, error) {
	return p.factory.endpoint(ctx, "verification_smapi_api_url", "https://smapi.x1m1.cn", "smapi.x1m1.cn")
}

func (p *smapiAdapter) headers(ctx context.Context) (map[string]string, error) {
	app, secret := p.factory.get(ctx, "verification_smapi_app_key"), p.factory.get(ctx, "verification_smapi_secret_key")
	if app == "" || secret == "" {
		return nil, errors.New("Smapi 配置不完整")
	}
	return map[string]string{"X-App-Key": app, "X-App-Secret": secret}, nil
}

func (p *smapiAdapter) Start(ctx context.Context, in VerificationStartRequest) (VerificationStartResult, error) {
	base, err := p.endpoint(ctx)
	if err != nil {
		return VerificationStartResult{}, err
	}
	headers, err := p.headers(ctx)
	if err != nil {
		return VerificationStartResult{}, err
	}
	body, _ := json.Marshal(map[string]string{"product_code": p.factory.get(ctx, "verification_smapi_product_code"), "cert_name": in.LegalName, "cert_no": in.IdentityNumber, "return_url": in.ReturnURL})
	var result map[string]any
	if err := p.factory.requestJSON(ctx, http.MethodPost, base+"/api/realname/initialize", body, headers, &result); err != nil {
		return VerificationStartResult{}, err
	}
	if !responseOK(result) {
		return VerificationStartResult{}, errors.New("Smapi 实名初始化失败")
	}
	data, _ := result["data"].(map[string]any)
	ref := stringValue(data, "id")
	if ref == "" {
		ref = stringValue(result, "id")
	}
	verifyURL := firstString(data, "certify_page_url", "certify_url", "url", "qrcode_url", "qr_code_url")
	if ref == "" {
		return VerificationStartResult{}, errors.New("Smapi 未返回认证编号")
	}
	return VerificationStartResult{ProviderRef: ref, URL: verifyURL}, nil
}

func (p *smapiAdapter) Poll(ctx context.Context, ref string) (VerificationStatus, error) {
	base, err := p.endpoint(ctx)
	if err != nil {
		return VerificationStatus{}, err
	}
	headers, err := p.headers(ctx)
	if err != nil {
		return VerificationStatus{}, err
	}
	var result map[string]any
	if err := p.factory.requestJSON(ctx, http.MethodGet, base+"/api/realname/certifications/"+url.PathEscape(ref)+"/query", nil, headers, &result); err != nil {
		return VerificationStatus{}, err
	}
	data, _ := result["data"].(map[string]any)
	status := strings.ToLower(firstString(data, "status"))
	switch status {
	case "passed", "success", "approved", "1":
		return VerificationStatus{Status: "approved"}, nil
	case "failed", "rejected", "2":
		return VerificationStatus{Status: "rejected", Message: firstString(data, "message", "fail_reason")}, nil
	default:
		return VerificationStatus{Status: "pending"}, nil
	}
}

func init() {
	registerVerificationAdapter("smapi", newSmapiAdapter)
}
