package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

// VerificationProvider 是第三方实名服务的最小边界。provider 不得直接修改用户表。
type VerificationProvider interface {
	Key() string
	Start(context.Context, VerificationStartRequest) (VerificationStartResult, error)
	Poll(context.Context, string) (VerificationStatus, error)
}

type VerificationStartRequest struct {
	LegalName      string
	IdentityNumber string
	ReturnURL      string
}

type VerificationStartResult struct {
	ProviderRef string
	URL         string
}

type VerificationStatus struct {
	Status  string // pending, approved, rejected, failed
	Message string
}

// ConfiguredVerificationProvider 根据后台选择的 provider 创建受信任的内置适配器。
type ConfiguredVerificationProvider struct {
	Settings *repo.Settings
	BaseURL  string
	Client   *http.Client
}

func NewConfiguredVerificationProvider(settings *repo.Settings, baseURL string) *ConfiguredVerificationProvider {
	return &ConfiguredVerificationProvider{Settings: settings, BaseURL: strings.TrimRight(baseURL, "/"), Client: &http.Client{Timeout: 15 * time.Second}}
}

func (f *ConfiguredVerificationProvider) get(ctx context.Context, key string) string {
	if f == nil || f.Settings == nil {
		return ""
	}
	v, _ := f.Settings.Get(ctx, key)
	return strings.TrimSpace(v)
}

func (f *ConfiguredVerificationProvider) Provider(ctx context.Context, key string) (VerificationProvider, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "baidu_face":
		return &baiduFaceProvider{factory: f}, nil
	case "leaf_face":
		return &leafFaceProvider{factory: f}, nil
	case "smapi":
		return &smapiProvider{factory: f}, nil
	case "stay33":
		return &stay33VerificationProvider{factory: f}, nil
	default:
		return nil, fmt.Errorf("实名 provider 未配置或不受支持")
	}
}

func (f *ConfiguredVerificationProvider) client() *http.Client {
	if f != nil && f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (f *ConfiguredVerificationProvider) endpoint(ctx context.Context, setting, fallback string, hosts ...string) (string, error) {
	raw := f.get(ctx, setting)
	if raw == "" {
		raw = fallback
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return "", errors.New("实名服务地址不安全")
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range hosts {
		if host == allowed {
			return strings.TrimRight(raw, "/"), nil
		}
	}
	return "", errors.New("实名服务地址不受支持")
}

func (f *ConfiguredVerificationProvider) requestJSON(ctx context.Context, method, endpoint string, body []byte, headers map[string]string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("实名服务请求无效")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return errors.New("实名服务连接失败")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || len(data) == 0 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("实名服务请求失败")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return errors.New("实名服务响应无效")
	}
	return nil
}

func (f *ConfiguredVerificationProvider) requestForm(ctx context.Context, endpoint string, values url.Values, headers map[string]string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return errors.New("实名服务请求无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return errors.New("实名服务连接失败")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || len(data) == 0 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("实名服务请求失败")
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return errors.New("实名服务响应无效")
	}
	return nil
}

// ---------------- 百度人脸 H5 ----------------

type baiduFaceProvider struct {
	factory *ConfiguredVerificationProvider
}

func (p *baiduFaceProvider) Key() string { return "baidu_face" }

func (p *baiduFaceProvider) Start(ctx context.Context, in VerificationStartRequest) (VerificationStartResult, error) {
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

func (p *baiduFaceProvider) Poll(ctx context.Context, ref string) (VerificationStatus, error) {
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

// ---------------- LeafFace ----------------

type leafFaceProvider struct {
	factory *ConfiguredVerificationProvider
}

func (p *leafFaceProvider) Key() string { return "leaf_face" }

func (p *leafFaceProvider) signedHeaders(ctx context.Context, method, path string, body []byte) (map[string]string, error) {
	appID, secret := p.factory.get(ctx, "verification_leaf_app_id"), p.factory.get(ctx, "verification_leaf_app_secret")
	if appID == "" || secret == "" {
		return nil, errors.New("LeafFace 配置不完整")
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, errors.New("生成实名请求随机数失败")
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := time.Now().UTC().Format(time.RFC3339)
	bodyHash := providerSHA256Hex(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + nonce + "\n" + bodyHash))
	return map[string]string{"X-App-Id": appID, "X-Timestamp": timestamp, "X-Nonce": nonce, "X-Body-Sha256": bodyHash, "X-Signature": hex.EncodeToString(mac.Sum(nil))}, nil
}

func (p *leafFaceProvider) base(ctx context.Context) (string, error) {
	return p.factory.endpoint(ctx, "verification_leaf_api_base", "https://face.ly-y.cn", "face.ly-y.cn")
}

func (p *leafFaceProvider) Start(ctx context.Context, in VerificationStartRequest) (VerificationStartResult, error) {
	base, err := p.base(ctx)
	if err != nil {
		return VerificationStartResult{}, err
	}
	var order [12]byte
	if _, err := rand.Read(order[:]); err != nil {
		return VerificationStartResult{}, errors.New("生成实名订单号失败")
	}
	body, _ := json.Marshal(map[string]any{"type": "h5_face", "real_name": in.LegalName, "card_no": in.IdentityNumber, "out_trade_no": "LF" + hex.EncodeToString(order[:]), "return_url": in.ReturnURL})
	headers, err := p.signedHeaders(ctx, http.MethodPost, "/api/merchant/verify/tasks", body)
	if err != nil {
		return VerificationStartResult{}, err
	}
	var result map[string]any
	if err := p.factory.requestJSON(ctx, http.MethodPost, base+"/api/merchant/verify/tasks", body, headers, &result); err != nil {
		return VerificationStartResult{}, err
	}
	ref := stringValue(result, "task_no")
	if ref == "" {
		if task, ok := result["task"].(map[string]any); ok {
			ref = stringValue(task, "task_no")
		}
	}
	verifyURL := stringValue(result, "verify_url")
	if ref == "" || verifyURL == "" {
		return VerificationStartResult{}, errors.New("LeafFace 未返回认证任务")
	}
	return VerificationStartResult{ProviderRef: ref, URL: verifyURL}, nil
}

func (p *leafFaceProvider) Poll(ctx context.Context, ref string) (VerificationStatus, error) {
	base, err := p.base(ctx)
	if err != nil {
		return VerificationStatus{}, err
	}
	path := "/api/merchant/verify/tasks/" + url.PathEscape(ref)
	headers, err := p.signedHeaders(ctx, http.MethodGet, path, nil)
	if err != nil {
		return VerificationStatus{}, err
	}
	var result map[string]any
	if err := p.factory.requestJSON(ctx, http.MethodGet, base+path, nil, headers, &result); err != nil {
		return VerificationStatus{}, err
	}
	status := strings.ToLower(stringValue(result, "status"))
	if task, ok := result["task"].(map[string]any); ok && status == "" {
		status = strings.ToLower(stringValue(task, "status"))
	}
	switch status {
	case "completed", "success", "passed":
		return VerificationStatus{Status: "approved"}, nil
	case "failed", "expired", "canceled", "cancelled":
		return VerificationStatus{Status: "rejected", Message: stringValue(result, "message")}, nil
	default:
		return VerificationStatus{Status: "pending"}, nil
	}
}

// ---------------- Smapi ----------------

type smapiProvider struct {
	factory *ConfiguredVerificationProvider
}

func (p *smapiProvider) Key() string { return "smapi" }

func (p *smapiProvider) endpoint(ctx context.Context) (string, error) {
	return p.factory.endpoint(ctx, "verification_smapi_api_url", "https://smapi.x1m1.cn", "smapi.x1m1.cn")
}

func (p *smapiProvider) headers(ctx context.Context) (map[string]string, error) {
	app, secret := p.factory.get(ctx, "verification_smapi_app_key"), p.factory.get(ctx, "verification_smapi_secret_key")
	if app == "" || secret == "" {
		return nil, errors.New("Smapi 配置不完整")
	}
	return map[string]string{"X-App-Key": app, "X-App-Secret": secret}, nil
}

func (p *smapiProvider) Start(ctx context.Context, in VerificationStartRequest) (VerificationStartResult, error) {
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

func (p *smapiProvider) Poll(ctx context.Context, ref string) (VerificationStatus, error) {
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

// ---------------- Stay33 实名 ----------------
type stay33VerificationProvider struct {
	factory *ConfiguredVerificationProvider
}

func (p *stay33VerificationProvider) Key() string { return "stay33" }

func (p *stay33VerificationProvider) endpoint(ctx context.Context) (string, error) {
	return p.factory.endpoint(ctx, "verification_stay33_api_url", "https://idc.stay33.cn/realname/certapi.php", "idc.stay33.cn")
}

func (p *stay33VerificationProvider) headers(ctx context.Context) (map[string]string, error) {
	apiKey, secret := p.factory.get(ctx, "verification_stay33_api_key"), p.factory.get(ctx, "verification_stay33_secret_key")
	if apiKey == "" || secret == "" {
		return nil, errors.New("Stay33 实名配置不完整")
	}
	return map[string]string{"api": apiKey, "key": secret}, nil
}

func (p *stay33VerificationProvider) Start(ctx context.Context, in VerificationStartRequest) (VerificationStartResult, error) {
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

func (p *stay33VerificationProvider) Poll(ctx context.Context, ref string) (VerificationStatus, error) {
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

func statusFromMap(m map[string]any) VerificationStatus {
	candidates := []string{"status", "verify_status", "auth_status", "conclusion", "verify_result"}
	var value string
	for _, key := range candidates {
		if v, ok := m[key]; ok {
			switch x := v.(type) {
			case string:
				value = strings.ToLower(strings.TrimSpace(x))
			case float64:
				value = strconv.Itoa(int(x))
			case bool:
				if x {
					value = "true"
				}
			}
			if value != "" {
				break
			}
		}
	}
	if result, ok := m["result"].(map[string]any); ok && value == "" {
		return statusFromMap(result)
	}
	switch value {
	case "success", "passed", "pass", "approved", "1", "true":
		return VerificationStatus{Status: "approved"}
	case "failed", "failure", "rejected", "2", "-1", "false":
		return VerificationStatus{Status: "rejected"}
	default:
		return VerificationStatus{Status: "pending"}
	}
}

func responseOK(m map[string]any) bool {
	if m == nil {
		return false
	}
	known := false
	for _, key := range []string{"status", "code"} {
		if raw, exists := m[key]; exists {
			known = true
			switch v := raw.(type) {
			case float64:
				return v == 200 || v == 20000 || v == 1
			case string:
				return v == "200" || v == "20000" || strings.EqualFold(v, "ok") || v == "1"
			}
		}
	}
	if v, ok := m["success"].(bool); ok {
		return v
	}
	if known {
		return false
	}
	return false
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := stringValue(m, key); v != "" {
			return v
		}
	}
	return ""
}

func providerSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func providerRandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
