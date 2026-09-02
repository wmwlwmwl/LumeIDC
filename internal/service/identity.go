package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"mime/multipart"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"lumeidc/internal/crypto"
	"lumeidc/internal/repo"
	"lumeidc/internal/storage"
)

var ErrIdentityRequired = errors.New("请先完成实名认证后再购买或续费")

// PhoneOTPProvider 隔离短信供应商；正式供应商接入后只替换实现。
type PhoneOTPProvider interface {
	Send(ctx context.Context, phone, code string) error
}

type UnavailablePhoneOTPProvider struct{}

func (UnavailablePhoneOTPProvider) Send(context.Context, string, string) error {
	return errors.New("短信服务暂未配置")
}

// MemoryPhoneOTPProvider 仅供测试和本地联调使用，不得用于生产。
type MemoryPhoneOTPProvider struct {
	mu    sync.Mutex
	codes map[string]string
}

func NewMemoryPhoneOTPProvider() *MemoryPhoneOTPProvider {
	return &MemoryPhoneOTPProvider{codes: make(map[string]string)}
}

func (p *MemoryPhoneOTPProvider) Send(_ context.Context, phone, code string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.codes[phone] = code
	return nil
}

func (p *MemoryPhoneOTPProvider) Code(phone string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.codes[phone]
}

type Identity struct {
	Store        *repo.IdentityStore
	Users        *repo.Users
	PII          *crypto.Cryptor
	Files        *storage.PrivateFiles
	OTP          PhoneOTPProvider
	Notifier     *Notifier
	Settings     *repo.Settings
	Verification *ConfiguredVerificationProvider
	BaseURL      string
	indexKey     []byte
}

func NewIdentity(store *repo.IdentityStore, users *repo.Users, pii *crypto.Cryptor, files *storage.PrivateFiles, otp PhoneOTPProvider, keyMaterial string, notifier *Notifier, settings *repo.Settings, baseURL string, verification *ConfiguredVerificationProvider) *Identity {
	h := sha256.Sum256([]byte("lumeidc identity index v1:" + keyMaterial))
	if otp == nil {
		otp = UnavailablePhoneOTPProvider{}
	}
	if verification == nil {
		verification = NewConfiguredVerificationProvider(settings, baseURL)
	}
	return &Identity{Store: store, Users: users, PII: pii, Files: files, OTP: otp, Notifier: notifier, Settings: settings, Verification: verification, BaseURL: baseURL, indexKey: h[:]}
}

var mainlandPhone = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
var identityNumber = regexp.MustCompile(`^(?:[0-9]{15}|[0-9]{17}[0-9Xx])$`)

var identityWeights = [...]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
var identityChecks = "10X98765432"

func normalizeIdentityNumber(raw string) (string, error) {
	id := strings.ToUpper(strings.TrimSpace(raw))
	if !identityNumber.MatchString(id) {
		return "", errors.New("身份证号格式不正确")
	}
	if len(id) == 15 {
		id = id[:6] + "19" + id[6:]
		if len(id) != 17 {
			return "", errors.New("身份证号格式不正确")
		}
		sum := 0
		for i := range identityWeights {
			sum += int(id[i]-'0') * identityWeights[i]
		}
		id += string(identityChecks[sum%11])
	}
	if len(id) != 18 {
		return "", errors.New("身份证号格式不正确")
	}
	sum := 0
	for i := range identityWeights {
		sum += int(id[i]-'0') * identityWeights[i]
	}
	if identityChecks[sum%11] != id[17] {
		return "", errors.New("身份证号校验码不正确")
	}
	return id, nil
}

func NormalizePhone(raw string) (string, error) {
	phone := strings.TrimSpace(raw)
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	if strings.HasPrefix(phone, "+86") {
		phone = phone[3:]
	} else if strings.HasPrefix(phone, "86") && len(phone) == 13 {
		phone = phone[2:]
	}
	if !mainlandPhone.MatchString(phone) {
		return "", errors.New("手机号格式不正确")
	}
	return "+86" + phone, nil
}

func MaskPhone(phone string) string {
	if len(phone) < 8 {
		return "已绑定"
	}
	return phone[:len(phone)-8] + "****" + phone[len(phone)-4:]
}

func (s *Identity) codeHMAC(phone, purpose, code string) string {
	m := hmac.New(sha256.New, s.indexKey)
	m.Write([]byte(purpose))
	m.Write([]byte{0})
	m.Write([]byte(phone))
	m.Write([]byte{0})
	m.Write([]byte(code))
	return fmt.Sprintf("%x", m.Sum(nil))
}

func (s *Identity) RequestPhoneCode(ctx context.Context, userID int64, rawPhone, purpose, ip string) error {
	if purpose != "bind" && purpose != "change" {
		return errors.New("验证码用途无效")
	}
	phone, err := NormalizePhone(rawPhone)
	if err != nil {
		return err
	}
	codeN, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return errors.New("生成验证码失败")
	}
	code := fmt.Sprintf("%06d", codeN.Int64())
	now := time.Now()
	id, err := s.Store.CreatePhoneChallenge(ctx, userID, purpose, phone, s.codeHMAC(phone, purpose, code), ip, now.Add(5*time.Minute), now)
	if err != nil {
		if errors.Is(err, repo.ErrPhoneInUse) {
			return errors.New("手机号不可用")
		}
		return err
	}
	if err := s.OTP.Send(ctx, phone, code); err != nil {
		_ = s.Store.InvalidatePhoneChallenge(ctx, id, time.Now())
		return errors.New("短信发送失败，请稍后再试")
	}
	return nil
}

func (s *Identity) ConfirmPhoneCode(ctx context.Context, userID int64, purpose, rawCode string) error {
	code := strings.TrimSpace(rawCode)
	if len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return repo.ErrChallengeCode
	}
	phone, _, err := s.Store.UserPhone(ctx, userID)
	if err != nil {
		return err
	}
	// change 的手机号从挑战记录中读取；这里仅用当前手机号构造 bind 的校验，实际消费由仓储锁定挑战。
	if purpose == "bind" && phone != "" {
		return errors.New("账户已绑定手机号，请使用换绑流程")
	}
	// 仓储层需要挑战中的目标手机号；先取当前 pending 挑战的手机号。
	target, err := s.Store.PendingPhone(ctx, userID, purpose)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.ErrChallengeInvalid
		}
		return err
	}
	_, err = s.Store.ConsumePhoneChallenge(ctx, userID, purpose, s.codeHMAC(target, purpose, code), time.Now())
	return err
}

func validLegalName(name string) bool {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) < 2 || len(runes) > 50 {
		return false
	}
	for _, r := range runes {
		if unicode.IsControl(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// RealNameForm 用于把 HTTP 文件头传给领域服务，避免将 multipart 逻辑散落在仓储层。
type RealNameForm struct {
	LegalName      string
	IdentityNumber string
	Front          *multipart.FileHeader
	Back           *multipart.FileHeader
}

func (s *Identity) manualRequiresPhone(ctx context.Context) bool {
	if s.Settings == nil {
		return true
	}
	v, err := s.Settings.Get(ctx, "manual_identity_requires_verified_phone")
	return err != nil || v != "0"
}

func (s *Identity) SubmitForm(ctx context.Context, userID int64, form RealNameForm) error {
	if s == nil || s.Store == nil {
		return errors.New("人工实名服务未配置")
	}
	if s.Settings != nil {
		if enabled, err := s.Settings.Get(ctx, "manual_identity_enabled"); err == nil && enabled == "0" {
			return errors.New("人工实名审核未启用")
		}
	}
	name := strings.TrimSpace(form.LegalName)
	if !validLegalName(name) {
		return errors.New("法定姓名格式不正确")
	}
	idNumber, err := normalizeIdentityNumber(form.IdentityNumber)
	if err != nil {
		return err
	}
	if form.Front == nil || form.Back == nil {
		return errors.New("请上传身份证正反面照片")
	}
	if s.PII == nil || s.Files == nil {
		return errors.New("实名资料服务未配置")
	}
	nameCipher, err := s.PII.Encrypt(name)
	if err != nil {
		return errors.New("加密实名资料失败")
	}
	idCipher, err := s.PII.Encrypt(idNumber)
	if err != nil {
		return errors.New("加密实名资料失败")
	}
	h := hmac.New(sha256.New, s.indexKey)
	h.Write([]byte(idNumber))
	identityHMAC := fmt.Sprintf("%x", h.Sum(nil))
	frontRef, err := s.Files.SavePhoto(form.Front)
	if err != nil {
		return err
	}
	backRef, err := s.Files.SavePhoto(form.Back)
	if err != nil {
		_ = s.Files.Delete(frontRef)
		return err
	}
	if err := s.Store.CreateSubmission(ctx, userID, nameCipher, idCipher, identityHMAC, frontRef, backRef, time.Now(), s.manualRequiresPhone(ctx)); err != nil {
		_ = s.Files.Delete(frontRef)
		_ = s.Files.Delete(backRef)
		return err
	}
	if s.Notifier != nil {
		s.Notifier.Notify(ctx, userID, "实名申请已提交", "你的实名资料已提交，等待管理员人工审核。")
	}
	return nil
}

func (s *Identity) Current(ctx context.Context, userID int64) (*repo.RealNameSubmission, error) {
	if s == nil || s.Store == nil {
		return nil, errors.New("实名服务未配置")
	}
	return s.Store.CurrentVerification(ctx, userID)
}

func (s *Identity) IsApproved(ctx context.Context, userID int64) (bool, error) {
	if s.Store == nil {
		return false, errors.New("实名服务未配置")
	}
	return s.Store.HasApproved(ctx, userID)
}

func (s *Identity) CurrentAutomatic(ctx context.Context, userID int64) (*repo.AutomaticIdentityAttempt, error) {
	if s.Store == nil {
		return nil, errors.New("实名服务未配置")
	}
	return s.Store.CurrentAutomatic(ctx, userID)
}

// StartProvider 创建一个受信任实名插件的本地 pending 记录。插件只返回任务引用，不能直接改用户表。
func (s *Identity) StartProvider(ctx context.Context, userID int64, providerKey string, form RealNameForm, returnURL string) (int64, string, error) {
	if s.Verification == nil || s.Store == nil {
		return 0, "", errors.New("实名插件服务未配置")
	}
	providerKey = strings.ToLower(strings.TrimSpace(providerKey))
	if providerKey == "" || providerKey == "manual" {
		return 0, "", errors.New("实名 provider 无效")
	}
	if existing, err := s.Store.CurrentAutomatic(ctx, userID); err != nil {
		return 0, "", err
	} else if existing != nil {
		if existing.Status == "pending" || existing.Status == "initiated" {
			return 0, "", repo.ErrVerificationBusy
		}
		if existing.Status == "approved" {
			return 0, "", repo.ErrVerificationDone
		}
	}
	name := strings.TrimSpace(form.LegalName)
	if !validLegalName(name) {
		return 0, "", errors.New("法定姓名格式不正确")
	}
	idNumber, err := normalizeIdentityNumber(form.IdentityNumber)
	if err != nil {
		return 0, "", err
	}
	if s.PII == nil {
		return 0, "", errors.New("实名资料加密器未配置")
	}
	provider, err := s.Verification.Provider(ctx, providerKey)
	if err != nil {
		return 0, "", err
	}
	started, err := provider.Start(ctx, VerificationStartRequest{LegalName: name, IdentityNumber: idNumber, ReturnURL: returnURL})
	if err != nil {
		return 0, "", err
	}
	if started.ProviderRef == "" {
		return 0, "", errors.New("实名插件未返回任务编号")
	}
	if started.URL != "" {
		u, err := url.Parse(started.URL)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || strings.ContainsAny(started.URL, "\r\n") {
			return 0, "", errors.New("实名插件返回地址不安全")
		}
		host := strings.ToLower(u.Hostname())
		allowed := map[string]string{"baidu_face": "brain.baidu.com", "leaf_face": "face.ly-y.cn", "smapi": "smapi.x1m1.cn", "stay33": "idc.stay33.cn"}[providerKey]
		if allowed != "" && host != allowed {
			return 0, "", errors.New("实名插件返回地址不受支持")
		}
	}
	nameCipher, err := s.PII.Encrypt(name)
	if err != nil {
		return 0, "", errors.New("加密实名资料失败")
	}
	idCipher, err := s.PII.Encrypt(idNumber)
	if err != nil {
		return 0, "", errors.New("加密实名资料失败")
	}
	mac := hmac.New(sha256.New, s.indexKey)
	mac.Write([]byte(idNumber))
	createdID, err := s.Store.CreatePluginSubmission(ctx, userID, providerKey, started.ProviderRef, started.URL, nameCipher, idCipher, fmt.Sprintf("%x", mac.Sum(nil)), time.Now())
	if err != nil {
		return 0, "", err
	}
	return createdID, started.URL, nil
}

func (s *Identity) PollProvider(ctx context.Context, userID int64, submissionID int64) error {
	v, err := s.Store.AutomaticAttempt(ctx, userID, submissionID)
	if err != nil || v.ProviderRef == "" {
		return errors.New("实名任务不存在")
	}
	provider, err := s.Verification.Provider(ctx, v.ProviderKey)
	if err != nil {
		return err
	}
	status, err := provider.Poll(ctx, v.ProviderRef)
	if err != nil {
		return err
	}
	return s.Store.UpdateProviderStatus(ctx, userID, v.ProviderRef, status.Status, status.Message, time.Now())
}

func (s *Identity) AdminSubmission(ctx context.Context, id int64) (*repo.RealNameSubmission, string, string, error) {
	v, err := s.Store.Submission(ctx, id)
	if err != nil {
		return nil, "", "", err
	}
	if s.PII == nil {
		return nil, "", "", errors.New("实名资料加密器未配置")
	}
	name, err := s.PII.Decrypt(v.LegalNameCiphertext)
	if err != nil {
		return nil, "", "", errors.New("实名资料解密失败")
	}
	idNumber, err := s.PII.Decrypt(v.IdentityNumberCiphertext)
	if err != nil {
		return nil, "", "", errors.New("实名资料解密失败")
	}
	return v, name, idNumber, nil
}

func (s *Identity) Review(ctx context.Context, id, adminID int64, approve bool, reason string) error {
	reason = strings.TrimSpace(reason)
	if !approve && (reason == "" || len([]rune(reason)) > 255) {
		return errors.New("拒绝原因不能为空且不能超过255字")
	}
	if err := s.Store.Review(ctx, id, adminID, approve, reason, time.Now()); err != nil {
		return err
	}
	v, err := s.Store.Submission(ctx, id)
	if err == nil && s.Notifier != nil {
		if approve {
			s.Notifier.Notify(ctx, v.UserID, "实名审核已通过", "你的实名资料已通过人工审核。")
		} else {
			s.Notifier.Notify(ctx, v.UserID, "实名审核未通过", "你的实名资料未通过人工审核，请登录账户查看原因并重新提交。")
		}
	}
	return nil
}

func StatusText(status string) string {
	switch status {
	case "pending":
		return "待人工审核"
	case "approved":
		return "已通过"
	case "rejected", "failed":
		return "未通过"
	case "expired":
		return "已过期"
	default:
		return "未提交"
	}
}

func MaskIdentityNumber(id string) string {
	r := []rune(id)
	if len(r) <= 8 {
		return "已提交"
	}
	return string(r[:4]) + strings.Repeat("*", len(r)-8) + string(r[len(r)-4:])
}
