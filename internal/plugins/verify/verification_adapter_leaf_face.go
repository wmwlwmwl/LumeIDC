package verify

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

	"lumeidc/internal/vsdk"
)

// leafFaceAdapter LeafFace 人脸实名适配器。
type leafFaceAdapter struct {
	host *vsdk.Host
}

func newLeafFaceAdapter(h *vsdk.Host) (vsdk.Provider, error) {
	return &leafFaceAdapter{host: h}, nil
}

func (p *leafFaceAdapter) Key() string { return "leaf_face" }

func (p *leafFaceAdapter) signedHeaders(ctx context.Context, method, path string, body []byte) (map[string]string, error) {
	appID, secret := p.host.Get(ctx, "verification_leaf_app_id"), p.host.Get(ctx, "verification_leaf_app_secret")
	if appID == "" || secret == "" {
		return nil, errors.New("LeafFace 配置不完整")
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return nil, errors.New("生成实名请求随机数失败")
	}
	nonce := hex.EncodeToString(nonceBytes)
	timestamp := time.Now().UTC().Format(time.RFC3339)
	bodyHash := vsdk.SHA256Hex(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + nonce + "\n" + bodyHash))
	return map[string]string{"X-App-Id": appID, "X-Timestamp": timestamp, "X-Nonce": nonce, "X-Body-Sha256": bodyHash, "X-Signature": hex.EncodeToString(mac.Sum(nil))}, nil
}

func (p *leafFaceAdapter) base(ctx context.Context) (string, error) {
	return p.host.Endpoint(ctx, "verification_leaf_api_base", "https://face.ly-y.cn", "face.ly-y.cn")
}

func (p *leafFaceAdapter) Start(ctx context.Context, in vsdk.StartRequest) (vsdk.StartResult, error) {
	base, err := p.base(ctx)
	if err != nil {
		return vsdk.StartResult{}, err
	}
	var order [12]byte
	if _, err := rand.Read(order[:]); err != nil {
		return vsdk.StartResult{}, errors.New("生成实名订单号失败")
	}
	body, _ := json.Marshal(map[string]any{"type": "h5_face", "real_name": in.LegalName, "card_no": in.IdentityNumber, "out_trade_no": "LF" + hex.EncodeToString(order[:]), "return_url": in.ReturnURL})
	headers, err := p.signedHeaders(ctx, http.MethodPost, "/api/merchant/verify/tasks", body)
	if err != nil {
		return vsdk.StartResult{}, err
	}
	var result map[string]any
	if err := p.host.RequestJSON(ctx, http.MethodPost, base+"/api/merchant/verify/tasks", body, headers, &result); err != nil {
		return vsdk.StartResult{}, err
	}
	ref := vsdk.StringValue(result, "task_no")
	if ref == "" {
		if task, ok := result["task"].(map[string]any); ok {
			ref = vsdk.StringValue(task, "task_no")
		}
	}
	verifyURL := vsdk.StringValue(result, "verify_url")
	if ref == "" || verifyURL == "" {
		return vsdk.StartResult{}, errors.New("LeafFace 未返回认证任务")
	}
	return vsdk.StartResult{ProviderRef: ref, URL: verifyURL}, nil
}

func (p *leafFaceAdapter) Poll(ctx context.Context, ref string) (vsdk.Status, error) {
	base, err := p.base(ctx)
	if err != nil {
		return vsdk.Status{}, err
	}
	path := "/api/merchant/verify/tasks/" + url.PathEscape(ref)
	headers, err := p.signedHeaders(ctx, http.MethodGet, path, nil)
	if err != nil {
		return vsdk.Status{}, err
	}
	var result map[string]any
	if err := p.host.RequestJSON(ctx, http.MethodGet, base+path, nil, headers, &result); err != nil {
		return vsdk.Status{}, err
	}
	status := strings.ToLower(vsdk.StringValue(result, "status"))
	if task, ok := result["task"].(map[string]any); ok && status == "" {
		status = strings.ToLower(vsdk.StringValue(task, "status"))
	}
	switch status {
	case "completed", "success", "passed":
		return vsdk.Status{Status: "approved"}, nil
	case "failed", "expired", "canceled", "cancelled":
		return vsdk.Status{Status: "rejected", Message: vsdk.StringValue(result, "message")}, nil
	default:
		return vsdk.Status{Status: "pending"}, nil
	}
}

var descriptorLeafFace = vsdk.Descriptor{
	Key:  "leaf_face",
	Name: "叶子人脸",
	Fields: []vsdk.ConfigField{
		{Key: "verification_leaf_app_id", Label: "叶子 App ID"},
		{Key: "verification_leaf_api_base", Label: "接口地址"},
		{Key: "verification_leaf_app_secret", Label: "叶子 App Secret", Secret: true},
	},
	ReturnHost: "face.ly-y.cn",
}

func init() {
	vsdk.Register(descriptorLeafFace, newLeafFaceAdapter)
}
