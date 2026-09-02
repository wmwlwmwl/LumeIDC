package integration

import (
	"context"
	"errors"
	"testing"
)

type panicExecutor struct{}

func (panicExecutor) Execute(context.Context, string, map[string]any) (Result, error) {
	panic("test panic")
}

type okExecutor struct{}

func (okExecutor) Execute(context.Context, string, map[string]any) (Result, error) {
	return Result{OK: true, Status: "approved"}, nil
}

func TestRegistryContainsProviderAndNormalizesPanic(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Manifest{Domain: DomainCaptcha, Key: "test"}, okExecutor{}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Execute(context.Background(), DomainCaptcha, "test", "verify", nil)
	if err != nil || !got.OK || got.Status != "approved" {
		t.Fatalf("unexpected result: %#v, %v", got, err)
	}
	if _, err := r.Execute(context.Background(), DomainCaptcha, "missing", "verify", nil); err == nil {
		t.Fatal("missing provider should fail")
	}
	if err := r.Register(Manifest{Domain: DomainCaptcha, Key: "panic"}, panicExecutor{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(context.Background(), DomainCaptcha, "panic", "verify", nil); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("panic should be normalized to provider error")
	}
}
