package verify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"lumeidc/internal/vsdk"
)

// smapiAdapter Smapi 实名适配器。
type smapiAdapter struct {
	host *vsdk.Host
}

func newSmapiAdapter(h *vsdk.Host) (vsdk.Provider, error) {
	return &smapiAdapter{host: h}, nil
}

func (p *smapiAdapter) Key() string { return "smapi" }

func (p *smapiAdapter) endpoint(ctx context.Context) (string, error) {
	return p.host.Endpoint(ctx, "verification_smapi_api_url", "https://smapi.x1m1.cn", "smapi.x1m1.cn")
}

func (p *smapiAdapter) headers(ctx context.Context) (map[string]string, error) {
	app, secret := p.host.Get(ctx, "verification_smapi_app_key"), p.host.Get(ctx, "verification_smapi_secret_key")
	if app == "" || secret == "" {
		return nil, errors.New("Smapi 配置不完整")
	}
	return map[string]string{"X-App-Key": app, "X-App-Secret": secret}, nil
}

func (p *smapiAdapter) Start(ctx context.Context, in vsdk.StartRequest) (vsdk.StartResult, error) {
	base, err := p.endpoint(ctx)
	if err != nil {
		return vsdk.StartResult{}, err
	}
	headers, err := p.headers(ctx)
	if err != nil {
		return vsdk.StartResult{}, err
	}
	body, _ := json.Marshal(map[string]string{"product_code": p.host.Get(ctx, "verification_smapi_product_code"), "cert_name": in.LegalName, "cert_no": in.IdentityNumber, "return_url": in.ReturnURL})
	var result map[string]any
	if err := p.host.RequestJSON(ctx, http.MethodPost, base+"/api/realname/initialize", body, headers, &result); err != nil {
		return vsdk.StartResult{}, err
	}
	if !vsdk.ResponseOK(result) {
		return vsdk.StartResult{}, errors.New("Smapi 实名初始化失败")
	}
	data, _ := result["data"].(map[string]any)
	ref := vsdk.StringValue(data, "id")
	if ref == "" {
		ref = vsdk.StringValue(result, "id")
	}
	verifyURL := vsdk.FirstString(data, "certify_page_url", "certify_url", "url", "qrcode_url", "qr_code_url")
	if ref == "" {
		return vsdk.StartResult{}, errors.New("Smapi 未返回认证编号")
	}
	return vsdk.StartResult{ProviderRef: ref, URL: verifyURL}, nil
}

func (p *smapiAdapter) Poll(ctx context.Context, ref string) (vsdk.Status, error) {
	base, err := p.endpoint(ctx)
	if err != nil {
		return vsdk.Status{}, err
	}
	headers, err := p.headers(ctx)
	if err != nil {
		return vsdk.Status{}, err
	}
	var result map[string]any
	if err := p.host.RequestJSON(ctx, http.MethodGet, base+"/api/realname/certifications/"+url.PathEscape(ref)+"/query", nil, headers, &result); err != nil {
		return vsdk.Status{}, err
	}
	data, _ := result["data"].(map[string]any)
	status := strings.ToLower(vsdk.FirstString(data, "status"))
	switch status {
	case "passed", "success", "approved", "1":
		return vsdk.Status{Status: "approved"}, nil
	case "failed", "rejected", "2":
		return vsdk.Status{Status: "rejected", Message: vsdk.FirstString(data, "message", "fail_reason")}, nil
	default:
		return vsdk.Status{Status: "pending"}, nil
	}
}

var descriptorSmapi = vsdk.Descriptor{
	Key:  "smapi",
	Name: "SMAPI",
	Fields: []vsdk.ConfigField{
		{Key: "verification_smapi_app_key", Label: "App Key"},
		{Key: "verification_smapi_api_url", Label: "接口地址"},
		{Key: "verification_smapi_product_code", Label: "产品编码"},
		{Key: "verification_smapi_secret_key", Label: "Secret Key", Secret: true},
	},
	ReturnHost: "smapi.x1m1.cn",
}

func init() {
	vsdk.Register(descriptorSmapi, newSmapiAdapter)
}
