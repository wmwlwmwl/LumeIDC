package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// leafFaceAdapter LeafFace 人脸实名适配器。
// 与原 leafFaceProvider 行为完全一致，通过 init() 注册到工厂。
type leafFaceAdapter struct {
	factory *ConfiguredVerificationProvider
}

func newLeafFaceAdapter(f *ConfiguredVerificationProvider) (VerificationProvider, error) {
	return &leafFaceAdapter{factory: f}, nil
}

func (p *leafFaceAdapter) Key() string { return "leaf_face" }

func (p *leafFaceAdapter) signedHeaders(ctx context.Context, method, path string, body []byte) (map[string]string, error) {
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

func (p *leafFaceAdapter) base(ctx context.Context) (string, error) {
	return p.factory.endpoint(ctx, "verification_leaf_api_base", "https://face.ly-y.cn", "face.ly-y.cn")
}

func (p *leafFaceAdapter) Start(ctx context.Context, in VerificationStartRequest) (VerificationStartResult, error) {
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

func (p *leafFaceAdapter) Poll(ctx context.Context, ref string) (VerificationStatus, error) {
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

func init() {
	registerVerificationAdapter("leaf_face", newLeafFaceAdapter)
}
