package service

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"138 0013 8000":  "+8613800138000",
		"+8613800138000": "+8613800138000",
		"8613800138000":  "+8613800138000",
	}
	for input, want := range cases {
		got, err := NormalizePhone(input)
		if err != nil || got != want {
			t.Fatalf("NormalizePhone(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "12345678901", "+85213800138000"} {
		if _, err := NormalizePhone(input); err == nil {
			t.Fatalf("NormalizePhone(%q) unexpectedly succeeded", input)
		}
	}
}

func TestNormalizeIdentityNumber(t *testing.T) {
	want := "11010519491231002X"
	for _, input := range []string{want, "110105491231002"} {
		got, err := normalizeIdentityNumber(input)
		if err != nil || got != want {
			t.Fatalf("normalizeIdentityNumber(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := normalizeIdentityNumber("110105194912310021"); err == nil {
		t.Fatal("invalid checksum unexpectedly succeeded")
	}
}

func TestIdentityMasks(t *testing.T) {
	if got := MaskPhone("+8613800138000"); got != "+86138****8000" {
		t.Fatalf("MaskPhone() = %q", got)
	}
	if got := MaskIdentityNumber("110101199001011234"); got != "1101**********1234" {
		t.Fatalf("MaskIdentityNumber() = %q", got)
	}
	if got := StatusText("pending"); got != "待人工审核" {
		t.Fatalf("StatusText() = %q", got)
	}
}

func TestValidLegalName(t *testing.T) {
	for _, name := range []string{"张三", "欧阳娜娜"} {
		if !validLegalName(name) {
			t.Fatalf("validLegalName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "A1", "张\n三"} {
		if validLegalName(name) {
			t.Fatalf("validLegalName(%q) = true", name)
		}
	}
}
