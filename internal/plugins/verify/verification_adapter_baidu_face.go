package verify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"lumeidc/internal/vsdk"
)

// baiduFaceAdapter 百度人脸 H5 实名适配器。
type baiduFaceAdapter struct {
	host *vsdk.Host
}

func newBaiduFaceAdapter(h *vsdk.Host) (vsdk.Provider, error) {
	return &baiduFaceAdapter{host: h}, nil
}

func (p *baiduFaceAdapter) Key() string { return "baidu_face" }

func (p *baiduFaceAdapter) Start(ctx context.Context, in vsdk.StartRequest) (vsdk.StartResult, error) {
	apiKey, secret := p.host.Get(ctx, "verification_baidu_api_key"), p.host.Get(ctx, "verification_baidu_secret_key")
	if apiKey == "" || secret == "" {
		return vsdk.StartResult{}, errors.New("百度人脸配置不完整")
	}
	tokenURL := "https://aip.baidubce.com/oauth/2.0/token?grant_type=client_credentials&client_id=" + url.QueryEscape(apiKey) + "&client_secret=" + url.QueryEscape(secret)
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := p.host.RequestJSON(ctx, http.MethodGet, tokenURL, nil, nil, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return vsdk.StartResult{}, errors.New("百度人脸授权失败")
	}
	plan, _ := strconv.Atoi(p.host.Get(ctx, "verification_baidu_plan_id"))
	if plan == 0 {
		plan = 25921
	}
	payload, _ := json.Marshal(map[string]any{"plan_id": plan, "redirect_config": map[string]string{"success_url": in.ReturnURL, "failed_url": in.ReturnURL}})
	genURL := "https://aip.baidubce.com/rpc/2.0/brain/solution/faceprint/verifyToken/generate?access_token=" + url.QueryEscape(tokenResp.AccessToken)
	var gen struct {
		Result struct {
			VerifyToken string `json:"verify_token"`
			Token       string `json:"token"`
		} `json:"result"`
	}
	if err := p.host.RequestJSON(ctx, http.MethodPost, genURL, payload, nil, &gen); err != nil {
		return vsdk.StartResult{}, errors.New("百度人脸认证初始化失败")
	}
	ref := gen.Result.VerifyToken
	if ref == "" {
		ref = gen.Result.Token
	}
	if ref == "" {
		return vsdk.StartResult{}, errors.New("百度人脸未返回认证编号")
	}
	submitURL := "https://aip.baidubce.com/rpc/2.0/brain/solution/faceprint/idcard/submit?access_token=" + url.QueryEscape(tokenResp.AccessToken)
	submitBody, _ := json.Marshal(map[string]any{"verify_token": ref, "id_name": in.LegalName, "id_no": in.IdentityNumber, "certificate_type": 0})
	var submit struct {
		ErrorCode int `json:"error_code"`
	}
	if err := p.host.RequestJSON(ctx, http.MethodPost, submitURL, submitBody, nil, &submit); err != nil || submit.ErrorCode != 0 {
		return vsdk.StartResult{}, errors.New("百度人脸资料提交失败")
	}
	return vsdk.StartResult{ProviderRef: ref, URL: "https://brain.baidu.com/face/print/?token=" + url.QueryEscape(ref)}, nil
}

func (p *baiduFaceAdapter) Poll(ctx context.Context, ref string) (vsdk.Status, error) {
	apiKey, secret := p.host.Get(ctx, "verification_baidu_api_key"), p.host.Get(ctx, "verification_baidu_secret_key")
	if apiKey == "" || secret == "" || ref == "" {
		return vsdk.Status{}, errors.New("百度人脸配置不完整")
	}
	tokenURL := "https://aip.baidubce.com/oauth/2.0/token?grant_type=client_credentials&client_id=" + url.QueryEscape(apiKey) + "&client_secret=" + url.QueryEscape(secret)
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := p.host.RequestJSON(ctx, http.MethodGet, tokenURL, nil, nil, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return vsdk.Status{}, errors.New("百度人脸授权失败")
	}
	endpoint := "https://aip.baidubce.com/rpc/2.0/brain/solution/faceprint/result/detail?access_token=" + url.QueryEscape(tokenResp.AccessToken)
	body, _ := json.Marshal(map[string]string{"verify_token": ref})
	var result map[string]any
	if err := p.host.RequestJSON(ctx, http.MethodPost, endpoint, body, nil, &result); err != nil {
		return vsdk.Status{}, err
	}
	return vsdk.StatusFromMap(result), nil
}

var descriptorBaiduFace = vsdk.Descriptor{
	Key:  "baidu_face",
	Name: "百度人脸",
	Fields: []vsdk.ConfigField{
		{Key: "verification_baidu_api_key", Label: "百度 API Key"},
		{Key: "verification_baidu_plan_id", Label: "百度方案 ID"},
		{Key: "verification_baidu_secret_key", Label: "百度 Secret Key", Secret: true},
	},
	ReturnHost: "brain.baidu.com",
}

func init() {
	vsdk.Register(descriptorBaiduFace, newBaiduFaceAdapter)
}
