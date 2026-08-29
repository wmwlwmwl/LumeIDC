package crypto

import (
	"encoding/base64"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(make([]byte, 32))
}

func TestRoundTrip(t *testing.T) {
	c, err := New(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"a", "u12345", "Ab3#xY9~!@", "密码测试123", string(make([]byte, 4096))} {
		enc, err := c.Encrypt(s)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", s, err)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != s {
			t.Fatalf("round-trip mismatch: got %q want %q", got, s)
		}
	}
}

func TestEmpty(t *testing.T) {
	c, _ := New(testKey(t))
	enc, err := c.Encrypt("")
	if enc != "" || err != nil {
		t.Fatalf("空明文应返回空串: %q %v", enc, err)
	}
	got, err := c.Decrypt("")
	if got != "" || err != nil {
		t.Fatalf("空密文应返回空串: %q %v", got, err)
	}
}

func TestWrongKey(t *testing.T) {
	c1, _ := New(testKey(t))
	enc, _ := c1.Encrypt("secret")
	c2, _ := New(testKey(t)) // 不同随机 key（全 0 的 key 相同，改用真实差异 key）
	k := make([]byte, 32)
	k[0] = 1
	c2, _ = New(base64.StdEncoding.EncodeToString(k))
	if _, err := c2.Decrypt(enc); err == nil {
		t.Fatal("密钥不符时应报错")
	}
}

func TestTamperedCiphertext(t *testing.T) {
	c, _ := New(testKey(t))
	enc, _ := c.Encrypt("secret")
	raw, _ := base64.StdEncoding.DecodeString(enc)
	raw[len(raw)-1] ^= 0xFF
	if _, err := c.Decrypt(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("密文被篡改时应报错")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New("not-base64!!!"); err == nil {
		t.Fatal("非法 base64 应报错")
	}
	if _, err := New(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Fatal("短密钥应报错")
	}
}

func TestNonceUnique(t *testing.T) {
	c, _ := New(testKey(t))
	e1, _ := c.Encrypt("same")
	e2, _ := c.Encrypt("same")
	if e1 == e2 {
		t.Fatal("相同明文两次加密应产生不同密文（随机 nonce）")
	}
}
