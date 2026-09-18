package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// baiduFaceAdapter 百度人脸 H5 实名适配器。
// 与原 baiduFaceProvider 行为完全一致，通过 init() 注册到工厂。
type baiduFaceAdapter struct {
	factory *ConfiguredVerificationProvider
}

func newBaiduFaceAdapter(f *ConfiguredVerificationProvider) (VerificationProvider, error) {
	return &baiduFaceAdapter{factory: f}, nil
}

func (p *baiduFaceAdapter) Key() string { return "baidu_face" }

func (p *baiduFaceAdapter) Start(ctx context.Context, in VerificationStartRequest) (VerificationStartResult, error) {
	apiKey, secret := p.factory.get(ctx, "verification_baidu_api_key"), p.factory.get(ctx, "verification_baidu_secret_key")
	if apiKey == "" || secret == "" {
		return VerificationStartResult{}, errors.New("百度人脸配置不完整")
	}
	tokenURL := "https://aip.baidubce.com/oauth/2.0/token?grant_type=client_credentials&client_id=" + url.QueryEscape(apiKey) + "&client_secret=" + url.QueryEscape(secret)
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := p.factory.requestJSON(ctx, http.MethodGet, tokenURL, nil, nil, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return VerificationStartResult{}, errors.New("百度人脸授权失败")
	}
	plan, _ := strconv.Atoi(p.factory.get(ctx, "verification_baidu_plan_id"))
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
	if err := p.factory.requestJSON(ctx, http.MethodPost, genURL, payload, nil, &gen); err != nil {
		return VerificationStartResult{}, errors.New("百度人脸认证初始化失败")
	}
	ref := gen.Result.VerifyToken
	if ref == "" {
		ref = gen.Result.Token
	}
	if ref == "" {
		return VerificationStartResult{}, errors.New("百度人脸未返回认证编号")
	}
	submitURL := "https://aip.baidubce.com/rpc/2.0/brain/solution/faceprint/idcard/submit?access_token=" + url.QueryEscape(tokenResp.AccessToken)
	submitBody, _ := json.Marshal(map[string]any{"verify_token": ref, "id_name": in.LegalName, "id_no": in.IdentityNumber, "certificate_type": 0})
	var submit struct {
		ErrorCode int `json:"error_code"`
	}
	if err := p.factory.requestJSON(ctx, http.MethodPost, submitURL, submitBody, nil, &submit); err != nil || submit.ErrorCode != 0 {
		return VerificationStartResult{}, errors.New("百度人脸资料提交失败")
	}
	return VerificationStartResult{ProviderRef: ref, URL: "https://brain.baidu.com/face/print/?token=" + url.QueryEscape(ref)}, nil
}

func (p *baiduFaceAdapter) Poll(ctx context.Context, ref string) (VerificationStatus, error) {
	apiKey, secret := p.factory.get(ctx, "verification_baidu_api_key"), p.factory.get(ctx, "verification_baidu_secret_key")
	if apiKey == "" || secret == "" || ref == "" {
		return VerificationStatus{}, errors.New("百度人脸配置不完整")
	}
	tokenURL := "https://aip.baidubce.com/oauth/2.0/token?grant_type=client_credentials&client_id=" + url.QueryEscape(apiKey) + "&client_secret=" + url.QueryEscape(secret)
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := p.factory.requestJSON(ctx, http.MethodGet, tokenURL, nil, nil, &tokenResp); err != nil || tokenResp.AccessToken == "" {
		return VerificationStatus{}, errors.New("百度人脸授权失败")
	}
	endpoint := "https://aip.baidubce.com/rpc/2.0/brain/solution/faceprint/result/detail?access_token=" + url.QueryEscape(tokenResp.AccessToken)
	body, _ := json.Marshal(map[string]string{"verify_token": ref})
	var result map[string]any
	if err := p.factory.requestJSON(ctx, http.MethodPost, endpoint, body, nil, &result); err != nil {
		return VerificationStatus{}, err
	}
	return statusFromMap(result), nil
}

func init() {
	registerVerificationAdapter("baidu_face", newBaiduFaceAdapter)
}
