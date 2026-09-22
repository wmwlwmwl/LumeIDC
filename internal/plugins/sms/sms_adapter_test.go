package sms

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// roundTripFunc 自定义 RoundTripper：拦截请求供断言并返回罐头响应。
type roundTripFunc func(r *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func cannedResponse(body string, code int) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// capturedRequest 返回拦截函数与指向被捕获请求的指针（闭包内重赋值，须解引用）。
// respBody 为返回给适配器的罐头响应体。
func capturedRequest(respBody string) (roundTripFunc, **http.Request) {
	var captured *http.Request
	return func(r *http.Request) (*http.Response, error) {
		captured = r.Clone(r.Context())
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			captured.Body = io.NopCloser(strings.NewReader(string(b)))
		}
		return cannedResponse(respBody, http.StatusOK), nil
	}, &captured
}

// ---------- smsbao ----------

func TestSmsbaoConfigMissing(t *testing.T) {
	p, _ := newSMSBaoAdapter(map[string]string{}, mockClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "您的验证码：1234"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "短信宝账号或密码未配置") {
		t.Fatalf("want missing-config error, got %v", err)
	}
}

func TestSmsbaoSendSuccess(t *testing.T) {
	fn, reqPtr := capturedRequest("0")
	p, _ := newSMSBaoAdapter(map[string]string{"sms_username": "u1", "sms_secret_key": "secret123"}, mockClient(fn))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "您的验证码：1234", SignName: "测试签名"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{}); err != nil {
		t.Fatalf("send err: %v", err)
	}
	req := *reqPtr
	q := req.URL.Query()
	if q.Get("u") != "u1" {
		t.Errorf("want u=u1, got %q", q.Get("u"))
	}
	if q.Get("m") != "13800138000" {
		t.Errorf("want m=13800138000, got %q", q.Get("m"))
	}
	if !strings.Contains(q.Get("c"), "测试签名") {
		t.Errorf("content missing sign, got %q", q.Get("c"))
	}
	if !strings.HasPrefix(req.URL.String(), "https://api.smsbao.com/sms?") {
		t.Errorf("bad endpoint: %s", req.URL.String())
	}
}

func TestSmsbaoRejected(t *testing.T) {
	p, _ := newSMSBaoAdapter(map[string]string{"sms_username": "u1", "sms_secret_key": "s"}, mockClient(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return cannedResponse("30", http.StatusOK), nil
		})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "code"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, nil); err == nil {
		t.Fatal("want error on rejected code 30")
	}
}

// ---------- stay33 ----------

func TestStay33ConfigMissing(t *testing.T) {
	p, _ := newStay33Adapter(map[string]string{}, mockClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "【签名】验证码1234"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "Stay33 短信配置不完整") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestStay33SendSuccess(t *testing.T) {
	fn, reqPtr := capturedRequest(`{"code":1}`)
	p, _ := newStay33Adapter(map[string]string{"sms_username": "u", "sms_secret_key": "k", "sms_sign_name": "S"}, mockClient(fn))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "验证码：1234", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{"code": "1234"}); err != nil {
		t.Fatalf("send err: %v", err)
	}
	req := *reqPtr
	b, _ := io.ReadAll(req.Body)
	form, _ := url.ParseQuery(string(b))
	if form.Get("username") != "u" || form.Get("key") != "k" || form.Get("phone") != "+8613800138000" {
		t.Errorf("bad form: %s", string(b))
	}
}

func TestStay33Rejected(t *testing.T) {
	p, _ := newStay33Adapter(map[string]string{"sms_username": "u", "sms_secret_key": "k", "sms_sign_name": "S"}, mockClient(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return cannedResponse(`{"code":0}`, http.StatusOK), nil
		})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "验证码：1234"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{"code": "1234"}); err == nil {
		t.Fatal("want error on code != 1")
	}
}

// ---------- idcsmart ----------

func TestIDCsmartConfigMissing(t *testing.T) {
	p, _ := newIDCsmartAdapter(map[string]string{}, mockClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "code", ID: "T1", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "智简短信账号或密钥未配置") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestIDCsmartSendSuccess(t *testing.T) {
	fn, reqPtr := capturedRequest(`{"status":"200"}`)
	p, _ := newIDCsmartAdapter(map[string]string{"sms_username": "u", "sms_secret_key": "k"}, mockClient(fn))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "验证码1234", ID: "T1", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{"code": "1234"}); err != nil {
		t.Fatalf("send err: %v", err)
	}
	req := *reqPtr
	if req.Header.Get("api") != "u" || req.Header.Get("key") != "k" {
		t.Errorf("bad auth headers: api=%s key=%s", req.Header.Get("api"), req.Header.Get("key"))
	}
	if !strings.Contains(req.URL.String(), "action=send") {
		t.Errorf("bad action: %s", req.URL.String())
	}
}

func TestIDCsmartTemplateCreate(t *testing.T) {
	p, _ := newIDCsmartAdapter(map[string]string{"sms_username": "u", "sms_secret_key": "k", "sms_sign_name": "S"}, mockClient(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return cannedResponse(`{"status":"200","template_id":"TMP123"}`, http.StatusOK), nil
		})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Name: "tpl", Content: "正文", SignName: "S", Kind: "notification"}
	res, err := p.TemplateCreate(context.Background(), tpl)
	if err != nil {
		t.Fatalf("create err: %v", err)
	}
	if res.ProviderTemplateID != "TMP123" {
		t.Errorf("want TMP123, got %q", res.ProviderTemplateID)
	}
}

// ---------- idcsmartpro（营销范围）----------

func TestIDCsmartProMarketingSend(t *testing.T) {
	p, _ := newIDCsmartProAdapter(map[string]string{"sms_username": "u", "sms_secret_key": "k", "sms_sign_name": "S"}, mockClient(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return cannedResponse(`{"status":"200"}`, http.StatusOK), nil
		})))
	tpl := SMSProviderTemplate{Range: SMSRangeMarketing, Content: "营销内容", ID: "T1", SignName: "S", Kind: "notification"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, nil); err != nil {
		t.Fatalf("marketing send err: %v", err)
	}
}

// ---------- submail ----------

func TestSubmailConfigMissing(t *testing.T) {
	p, _ := newSubmailAdapter(map[string]string{}, mockClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "code", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "赛邮当前范围的应用标识或密钥未配置") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestSubmailSendSuccess(t *testing.T) {
	fn, reqPtr := capturedRequest(`{"status":"success"}`)
	p, _ := newSubmailAdapter(map[string]string{"sms_access_key": "ak", "sms_secret_key": "sk", "sms_sign_name": "S"}, mockClient(fn))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "验证码1234", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{"code": "1234"}); err != nil {
		t.Fatalf("send err: %v", err)
	}
	req := *reqPtr
	b, _ := io.ReadAll(req.Body)
	form, _ := url.ParseQuery(string(b))
	if form.Get("appid") != "ak" {
		t.Errorf("want appid=ak, got %q", form.Get("appid"))
	}
}

// ---------- aliyun（号码认证）----------

func TestAliyunConfigMissing(t *testing.T) {
	p, _ := newAliyunAdapter(map[string]string{}, mockClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "otp", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{"code": "1234"}); err == nil ||
		!strings.Contains(err.Error(), "阿里云号码认证配置不完整") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestAliyunSendSuccess(t *testing.T) {
	fn, reqPtr := capturedRequest(`{"Code":"OK","Success":true}`)
	p, _ := newAliyunAdapter(map[string]string{"sms_access_key": "ak", "sms_secret_key": "sk", "sms_sign_name": "S"}, mockClient(fn))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "otp", ID: "100005", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{"code": "1234", "purpose": "login"}); err != nil {
		t.Fatalf("send err: %v", err)
	}
	req := *reqPtr
	b, _ := io.ReadAll(req.Body)
	body := string(b)
	if !strings.Contains(body, "Action=SendSmsVerifyCode") {
		t.Errorf("missing action in body: %s", body)
	}
	if !strings.Contains(body, "Signature=") {
		t.Errorf("missing signature in body: %s", body)
	}
}

// ---------- aliyun_sms ----------

func TestAliyunSMSConfigMissing(t *testing.T) {
	p, _ := newAliyunSMSAdapter(map[string]string{}, mockClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "code", ID: "T1", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "阿里云短信服务配置不完整") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestAliyunSMSSendSuccess(t *testing.T) {
	fn, reqPtr := capturedRequest(`{"Code":"OK"}`)
	p, _ := newAliyunSMSAdapter(map[string]string{"sms_access_key": "ak", "sms_secret_key": "sk", "sms_sign_name": "S"}, mockClient(fn))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "验证码1234", ID: "T1", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{"code": "1234"}); err != nil {
		t.Fatalf("send err: %v", err)
	}
	req := *reqPtr
	if !strings.HasPrefix(req.Header.Get("Authorization"), "ACS3-HMAC-SHA256") {
		t.Errorf("bad authorization header: %s", req.Header.Get("Authorization"))
	}
	if req.Header.Get("x-acs-action") != "SendSms" {
		t.Errorf("bad action header: %s", req.Header.Get("x-acs-action"))
	}
}

func TestAliyunSMSTemplateCreate(t *testing.T) {
	p, _ := newAliyunSMSAdapter(map[string]string{"sms_access_key": "ak", "sms_secret_key": "sk", "sms_sign_name": "S"}, mockClient(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return cannedResponse(`{"Code":"OK","TemplateCode":"TC123"}`, http.StatusOK), nil
		})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Name: "tpl", Content: "正文", SignName: "S", Kind: "notification"}
	res, err := p.TemplateCreate(context.Background(), tpl)
	if err != nil {
		t.Fatalf("create err: %v", err)
	}
	if res.ProviderTemplateID != "TC123" {
		t.Errorf("want TC123, got %q", res.ProviderTemplateID)
	}
}

// ---------- qcloudsms ----------

func TestQcloudConfigMissing(t *testing.T) {
	p, _ := newQcloudSMSAdapter(map[string]string{}, mockClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("should not issue request")
		return nil, nil
	})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "code", ID: "T1", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "腾讯云短信密钥未配置") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestQcloudSendSuccess(t *testing.T) {
	fn, reqPtr := capturedRequest(`{"Response":{"SendStatusSet":[{"Code":"Ok","SerialNo":"s1"}],"RequestId":"r1"}}`)
	p, _ := newQcloudSMSAdapter(map[string]string{"sms_access_key": "ak", "sms_secret_key": "sk", "sms_username": "app1", "sms_sign_name": "S"}, mockClient(fn))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "验证码", ID: "1001", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{"param1": "1234"}); err != nil {
		t.Fatalf("send err: %v", err)
	}
	req := *reqPtr
	if req.Header.Get("X-TC-Action") != "SendSms" {
		t.Errorf("bad action header: %s", req.Header.Get("X-TC-Action"))
	}
	if !strings.Contains(req.Header.Get("Authorization"), "TC3-HMAC-SHA256") {
		t.Errorf("bad authorization: %s", req.Header.Get("Authorization"))
	}
}

func TestQcloudSendRejected(t *testing.T) {
	p, _ := newQcloudSMSAdapter(map[string]string{"sms_access_key": "ak", "sms_secret_key": "sk", "sms_username": "app1", "sms_sign_name": "S"}, mockClient(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return cannedResponse(`{"Response":{"SendStatusSet":[{"Code":"LimitExceeded","Message":"over limit"}],"RequestId":"r1"}}`, http.StatusOK), nil
		})))
	tpl := SMSProviderTemplate{Range: SMSRangeCN, Kind: "notification", Content: "验证码", ID: "1001", SignName: "S"}
	if _, err := p.SendSMS(context.Background(), "13800138000", tpl, map[string]string{"param1": "1234"}); err == nil {
		t.Fatal("want error on rejected Code")
	}
}

func TestQcloudTemplateIDMustBePositiveInt(t *testing.T) {
	p, _ := newQcloudSMSAdapter(map[string]string{"sms_access_key": "ak", "sms_secret_key": "sk", "sms_sign_name": "S"}, mockClient(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("should not issue request")
			return nil, nil
		})))
	if _, err := p.TemplateQuery(context.Background(), "abc", SMSRangeCN); err == nil ||
		!strings.Contains(err.Error(), "腾讯云模板编码须为正整数") {
		t.Fatalf("want positive-int error, got %v", err)
	}
}
