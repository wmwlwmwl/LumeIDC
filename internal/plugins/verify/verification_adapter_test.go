package verify

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"lumeidc/internal/vsdk"
)

type roundTripFunc func(r *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func cannedResponse(body string, code int) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// fakeSettings 实现 vsdk.SettingsGetter，返回预设值。
type fakeSettings map[string]string

func (f fakeSettings) Get(_ context.Context, key string) (string, error) {
	return f[key], nil
}

func newHost(s fakeSettings, fn roundTripFunc) *vsdk.Host {
	return &vsdk.Host{
		Settings: s,
		Client:   &http.Client{Transport: fn},
	}
}

// ---------- baidu_face ----------

func TestBaiduFaceConfigMissing(t *testing.T) {
	h := newHost(fakeSettings{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	}))
	p, _ := newBaiduFaceAdapter(h)
	if _, err := p.Start(context.Background(), vsdk.StartRequest{LegalName: "张三", IdentityNumber: "110101199003071234"}); err == nil ||
		!strings.Contains(err.Error(), "百度人脸配置不完整") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestBaiduFaceStartSuccess(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_baidu_api_key":    "ak",
		"verification_baidu_secret_key": "sk",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "oauth/2.0/token"):
			return cannedResponse(`{"access_token":"token123"}`, http.StatusOK), nil
		case strings.Contains(r.URL.Path, "verifyToken/generate"):
			return cannedResponse(`{"result":{"verify_token":"vt123"}}`, http.StatusOK), nil
		case strings.Contains(r.URL.Path, "idcard/submit"):
			return cannedResponse(`{"error_code":0}`, http.StatusOK), nil
		}
		t.Fatalf("unexpected request: %s", r.URL.String())
		return nil, nil
	}))
	p, _ := newBaiduFaceAdapter(h)
	res, err := p.Start(context.Background(), vsdk.StartRequest{LegalName: "张三", IdentityNumber: "110101199003071234", ReturnURL: "https://example.com/cb"})
	if err != nil {
		t.Fatalf("start err: %v", err)
	}
	if res.ProviderRef != "vt123" {
		t.Errorf("want vt123, got %q", res.ProviderRef)
	}
	if !strings.Contains(res.URL, "brain.baidu.com") {
		t.Errorf("bad URL: %s", res.URL)
	}
}

func TestBaiduFacePollApproved(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_baidu_api_key":    "ak",
		"verification_baidu_secret_key": "sk",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "oauth/2.0/token") {
			return cannedResponse(`{"access_token":"t"}`, http.StatusOK), nil
		}
		return cannedResponse(`{"result":{"status":"success"}}`, http.StatusOK), nil
	}))
	p, _ := newBaiduFaceAdapter(h)
	st, err := p.Poll(context.Background(), "vt123")
	if err != nil {
		t.Fatalf("poll err: %v", err)
	}
	if st.Status != "approved" {
		t.Errorf("want approved, got %q", st.Status)
	}
}

// ---------- stay33 ----------

func TestStay33VerificationConfigMissing(t *testing.T) {
	h := newHost(fakeSettings{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	}))
	p, _ := newStay33VerificationAdapter(h)
	if _, err := p.Start(context.Background(), vsdk.StartRequest{LegalName: "张三", IdentityNumber: "110101199003071234"}); err == nil ||
		!strings.Contains(err.Error(), "Stay33 实名配置不完整") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestStay33VerificationStartSuccess(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_stay33_api_key":    "k",
		"verification_stay33_secret_key": "s",
		"verification_stay33_biz_code":   "biz",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(b))
		switch form.Get("action") {
		case "initialize":
			return cannedResponse(`{"status":"200","certify_id":"cid123"}`, http.StatusOK), nil
		case "certify":
			return cannedResponse(`{"status":"200","url":"https://idc.stay33.cn/verify?x=1"}`, http.StatusOK), nil
		}
		t.Fatalf("unexpected action: %s", form.Get("action"))
		return nil, nil
	}))
	p, _ := newStay33VerificationAdapter(h)
	res, err := p.Start(context.Background(), vsdk.StartRequest{LegalName: "张三", IdentityNumber: "110101199003071234", ReturnURL: "https://example.com/cb"})
	if err != nil {
		t.Fatalf("start err: %v", err)
	}
	if res.ProviderRef != "cid123" {
		t.Errorf("want cid123, got %q", res.ProviderRef)
	}
	if res.URL == "" {
		t.Error("want non-empty URL")
	}
}

func TestStay33VerificationPollApproved(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_stay33_api_key":    "k",
		"verification_stay33_secret_key": "s",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return cannedResponse(`{"status":200,"msg":"success"}`, http.StatusOK), nil
	}))
	p, _ := newStay33VerificationAdapter(h)
	st, err := p.Poll(context.Background(), "cid123")
	if err != nil {
		t.Fatalf("poll err: %v", err)
	}
	if st.Status != "approved" {
		t.Errorf("want approved, got %q", st.Status)
	}
}

func TestStay33VerificationPollPending(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_stay33_api_key":    "k",
		"verification_stay33_secret_key": "s",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return cannedResponse(`{"status":200,"msg":"审核中"}`, http.StatusOK), nil
	}))
	p, _ := newStay33VerificationAdapter(h)
	st, err := p.Poll(context.Background(), "cid123")
	if err != nil {
		t.Fatalf("poll err: %v", err)
	}
	if st.Status != "pending" {
		t.Errorf("want pending, got %q", st.Status)
	}
}

// ---------- smapi ----------

func TestSmapiConfigMissing(t *testing.T) {
	h := newHost(fakeSettings{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	}))
	p, _ := newSmapiAdapter(h)
	if _, err := p.Start(context.Background(), vsdk.StartRequest{LegalName: "张三", IdentityNumber: "110101199003071234"}); err == nil ||
		!strings.Contains(err.Error(), "Smapi 配置不完整") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestSmapiStartSuccess(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_smapi_app_key":      "app",
		"verification_smapi_secret_key":   "sec",
		"verification_smapi_product_code": "prod",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/api/realname/initialize") {
			t.Errorf("bad path: %s", r.URL.Path)
		}
		return cannedResponse(`{"status":"200","data":{"id":"rid123","certify_url":"https://smapi.x1m1.cn/v?x=1"}}`, http.StatusOK), nil
	}))
	p, _ := newSmapiAdapter(h)
	res, err := p.Start(context.Background(), vsdk.StartRequest{LegalName: "张三", IdentityNumber: "110101199003071234", ReturnURL: "https://example.com/cb"})
	if err != nil {
		t.Fatalf("start err: %v", err)
	}
	if res.ProviderRef != "rid123" {
		t.Errorf("want rid123, got %q", res.ProviderRef)
	}
	if res.URL == "" {
		t.Error("want non-empty URL")
	}
}

func TestSmapiPollApproved(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_smapi_app_key":    "app",
		"verification_smapi_secret_key": "sec",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/query") {
			t.Errorf("bad path: %s", r.URL.Path)
		}
		return cannedResponse(`{"status":"200","data":{"status":"passed"}}`, http.StatusOK), nil
	}))
	p, _ := newSmapiAdapter(h)
	st, err := p.Poll(context.Background(), "rid123")
	if err != nil {
		t.Fatalf("poll err: %v", err)
	}
	if st.Status != "approved" {
		t.Errorf("want approved, got %q", st.Status)
	}
}

func TestSmapiPollRejected(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_smapi_app_key":    "app",
		"verification_smapi_secret_key": "sec",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return cannedResponse(`{"status":"200","data":{"status":"failed","message":"人脸不匹配"}}`, http.StatusOK), nil
	}))
	p, _ := newSmapiAdapter(h)
	st, err := p.Poll(context.Background(), "rid123")
	if err != nil {
		t.Fatalf("poll err: %v", err)
	}
	if st.Status != "rejected" {
		t.Errorf("want rejected, got %q", st.Status)
	}
}

// ---------- leaf_face ----------

func TestLeafFaceConfigMissing(t *testing.T) {
	h := newHost(fakeSettings{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	}))
	p, _ := newLeafFaceAdapter(h)
	if _, err := p.Start(context.Background(), vsdk.StartRequest{LegalName: "张三", IdentityNumber: "110101199003071234"}); err == nil ||
		!strings.Contains(err.Error(), "LeafFace 配置不完整") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestLeafFaceStartSuccess(t *testing.T) {
	var captured *http.Request
	h := newHost(fakeSettings{
		"verification_leaf_app_id":     "aid",
		"verification_leaf_app_secret": "asec",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		captured = r.Clone(r.Context())
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			captured.Body = io.NopCloser(strings.NewReader(string(b)))
		}
		return cannedResponse(`{"task_no":"tn123","verify_url":"https://face.ly-y.cn/v?x=1"}`, http.StatusOK), nil
	}))
	p, _ := newLeafFaceAdapter(h)
	res, err := p.Start(context.Background(), vsdk.StartRequest{LegalName: "张三", IdentityNumber: "110101199003071234", ReturnURL: "https://example.com/cb"})
	if err != nil {
		t.Fatalf("start err: %v", err)
	}
	if res.ProviderRef != "tn123" {
		t.Errorf("want tn123, got %q", res.ProviderRef)
	}
	if res.URL == "" {
		t.Error("want non-empty URL")
	}
	// 断言 HMAC 签名头存在
	if captured.Header.Get("X-App-Id") != "aid" {
		t.Errorf("bad X-App-Id: %s", captured.Header.Get("X-App-Id"))
	}
	if captured.Header.Get("X-Signature") == "" {
		t.Error("missing X-Signature header")
	}
}

func TestLeafFacePollApproved(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_leaf_app_id":     "aid",
		"verification_leaf_app_secret": "asec",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return cannedResponse(`{"status":"completed"}`, http.StatusOK), nil
	}))
	p, _ := newLeafFaceAdapter(h)
	st, err := p.Poll(context.Background(), "tn123")
	if err != nil {
		t.Fatalf("poll err: %v", err)
	}
	if st.Status != "approved" {
		t.Errorf("want approved, got %q", st.Status)
	}
}

func TestLeafFacePollRejected(t *testing.T) {
	h := newHost(fakeSettings{
		"verification_leaf_app_id":     "aid",
		"verification_leaf_app_secret": "asec",
	}, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return cannedResponse(`{"status":"expired","message":"已过期"}`, http.StatusOK), nil
	}))
	p, _ := newLeafFaceAdapter(h)
	st, err := p.Poll(context.Background(), "tn123")
	if err != nil {
		t.Fatalf("poll err: %v", err)
	}
	if st.Status != "rejected" {
		t.Errorf("want rejected, got %q", st.Status)
	}
}
