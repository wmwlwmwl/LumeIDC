package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Domain string

const (
	DomainSMS          Domain = "sms"
	DomainCaptcha      Domain = "captcha"
	DomainVerification Domain = "verification"
	DomainMail         Domain = "mail"
	timeout                   = 15 * time.Second
)

type ConfigField struct {
	Key      string
	Label    string
	Secret   bool
	Required bool
}

type Manifest struct {
	Domain       Domain
	Key          string
	Name         string
	Version      string
	Capabilities []string
	Config       []ConfigField
}

type Result struct {
	OK          bool
	Message     string
	ProviderRef string
	Status      string
	ReplayKey   string
	Data        map[string]any
}

type Runtime interface {
	Execute(ctx context.Context, manifest Manifest, action string, payload map[string]any) (Result, error)
}

type Executor interface {
	Execute(ctx context.Context, action string, payload map[string]any) (Result, error)
}

type Registry struct {
	mu      sync.RWMutex
	entries map[Domain]map[string]entry
}

type entry struct {
	manifest Manifest
	executor Executor
}

func NewRegistry() *Registry { return &Registry{entries: make(map[Domain]map[string]entry)} }

func (r *Registry) Register(m Manifest, e Executor) error {
	if m.Domain == "" || m.Key == "" || e == nil {
		return errors.New("provider manifest 无效")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[m.Domain] == nil {
		r.entries[m.Domain] = make(map[string]entry)
	}
	if _, exists := r.entries[m.Domain][m.Key]; exists {
		return fmt.Errorf("provider 已注册: %s/%s", m.Domain, m.Key)
	}
	r.entries[m.Domain][m.Key] = entry{manifest: m, executor: e}
	return nil
}

func (r *Registry) Manifest(domain Domain, key string) (Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[domain][key]
	return e.manifest, ok
}

func (r *Registry) Execute(ctx context.Context, domain Domain, key, action string, payload map[string]any) (Result, error) {
	r.mu.RLock()
	e, ok := r.entries[domain][key]
	r.mu.RUnlock()
	if !ok || e.executor == nil {
		return Result{}, fmt.Errorf("provider 未配置: %s/%s", domain, key)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return safeExecute(ctx, e.executor, action, payload)
}

func safeExecute(ctx context.Context, e Executor, action string, payload map[string]any) (res Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			res = Result{}
			err = fmt.Errorf("provider 执行异常")
		}
	}()
	return e.Execute(ctx, action, payload)
}

// ManualVerification 是内置人工审核 provider 的标识，不执行外部请求。
type ManualVerification struct{}

func (ManualVerification) Execute(context.Context, string, map[string]any) (Result, error) {
	return Result{OK: true, Status: "pending"}, nil
}

type DisabledProvider struct{ Reason string }

func (p DisabledProvider) Execute(context.Context, string, map[string]any) (Result, error) {
	if p.Reason == "" {
		p.Reason = "provider 未配置"
	}
	return Result{}, errors.New(p.Reason)
}
