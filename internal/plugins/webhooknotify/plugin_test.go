package webhooknotify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 投递时签名头必须是 sha256=HMAC(secret, body)，事件头原样透传。
func TestPostOnceSignature(t *testing.T) {
	var gotSig, gotEvent string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-LumeIDC-Signature")
		gotEvent = r.Header.Get("X-LumeIDC-Event")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &Plugin{}
	body := []byte(`{"event":"test","data":{"msg":"hi"}}`)
	status, _, err := p.postOnce(config{URL: srv.URL, Secret: "s3cret"}, "test", body)
	if err != nil || status != 200 {
		t.Fatalf("投递失败: status=%d err=%v", status, err)
	}
	if gotEvent != "test" {
		t.Fatalf("事件头错误: %q", gotEvent)
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Fatalf("签名不匹配: got %q want %q", gotSig, want)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("请求体不一致: %q", string(gotBody))
	}
}

// 未配置 secret 时不带签名头。
func TestPostOnceNoSecretNoSignature(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-LumeIDC-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := &Plugin{}
	if _, _, err := p.postOnce(config{URL: srv.URL}, "test", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if gotSig != "" {
		t.Fatalf("无 secret 不应带签名头: %q", gotSig)
	}
}

func TestEventOn(t *testing.T) {
	c := config{Events: []string{"order.paid"}}
	if !c.eventOn("order.paid") || c.eventOn("service.created") {
		t.Fatal("eventOn 白名单判定错误")
	}
}
