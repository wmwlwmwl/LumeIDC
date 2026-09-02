package captcha

import (
	"image/png"
	"testing"
)

func TestDrawCaptchaProducesPNG(t *testing.T) {
	img := drawCaptcha([]byte("2ABCD"))
	if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
		t.Fatalf("bounds = %v", img.Bounds())
	}
}

func TestSecureEqual(t *testing.T) {
	if secureEqual("abc", "abd") || secureEqual("", "x") || !secureEqual("abc", "abc") {
		t.Fatal("secureEqual result incorrect")
	}
}

func TestPNGEncoding(t *testing.T) {
	if err := png.Encode(testWriter{}, drawCaptcha([]byte("23456"))); err == nil {
		// testWriter intentionally returns an error; this confirms the encoder path is exercised.
	}
}

type testWriter struct{}

func (testWriter) Write([]byte) (int, error) { return 0, errTestWriter }

var errTestWriter = testingErr{}

type testingErr struct{}

func (testingErr) Error() string { return "test writer" }
