package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

// AuthChallengeService provides rate-limited, one-time email/phone OTP challenges.
type AuthChallengeService struct {
	Store     *repo.AuthChallenges
	SMS       PhoneOTPProvider
	EmailSend func(context.Context, string, string, string) error
	Key       []byte
}

func (s *AuthChallengeService) Issue(ctx context.Context, channel, purpose, destination, ip string) error {
	if s == nil || s.Store == nil || len(s.Key) == 0 {
		return errors.New("验证码服务未配置")
	}
	if channel != "email" && channel != "phone" {
		return errors.New("验证码渠道无效")
	}
	if purpose == "" || destination == "" {
		return errors.New("验证码参数无效")
	}
	codeN, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return errors.New("生成验证码失败")
	}
	code := fmt.Sprintf("%06d", codeN.Int64())
	normalized := destination
	if channel == "email" {
		normalized, err = repo.NormalizeEmail(destination)
	} else {
		normalized, err = NormalizePhone(destination)
	}
	if err != nil {
		return err
	}
	destinationHMAC := s.digest("destination", channel, normalized)
	limited, err := s.Store.RateLimited(ctx, channel, destinationHMAC, ip, time.Now())
	if err != nil {
		return err
	}
	if limited {
		return errors.New("验证码发送过于频繁，请稍后再试")
	}
	codeHMAC := s.digest("code", channel, normalized+"\x00"+purpose+"\x00"+code)
	now := time.Now()
	id, err := s.Store.CreateAnonymous(ctx, channel, purpose, normalized, destinationHMAC, codeHMAC, ip, now.Add(5*time.Minute), now)
	if err != nil {
		return err
	}
	if channel == "phone" {
		if s.SMS == nil {
			_ = s.Store.Invalidate(ctx, id, time.Now())
			return errors.New("短信服务未配置")
		}
		if err := s.SMS.Send(ctx, normalized, code); err != nil {
			_ = s.Store.Invalidate(ctx, id, time.Now())
			return errors.New("验证码发送失败，请稍后重试")
		}
		return nil
	}
	if s.EmailSend == nil {
		_ = s.Store.Invalidate(ctx, id, time.Now())
		return errors.New("邮件服务未配置")
	}
	if err := s.EmailSend(ctx, normalized, "LumeIDC 验证码", "你的验证码是："+code+"，5分钟内有效。"); err != nil {
		_ = s.Store.Invalidate(ctx, id, time.Now())
		return errors.New("验证码发送失败，请稍后重试")
	}
	return nil
}

func (s *AuthChallengeService) Verify(ctx context.Context, channel, purpose, destination, code string) error {
	if s == nil || s.Store == nil || len(s.Key) == 0 {
		return repo.ErrAuthChallengeInvalid
	}
	if len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return repo.ErrAuthChallengeCode
	}
	normalized := destination
	var err error
	if channel == "email" {
		normalized, err = repo.NormalizeEmail(destination)
	} else {
		normalized, err = NormalizePhone(destination)
	}
	if err != nil {
		return repo.ErrAuthChallengeInvalid
	}
	return s.Store.ConsumeAnonymous(ctx, channel, purpose, s.digest("destination", channel, normalized), s.digest("code", channel, normalized+"\x00"+purpose+"\x00"+code), time.Now())
}

func (s *AuthChallengeService) digest(kind, channel, value string) string {
	mac := hmac.New(sha256.New, s.Key)
	mac.Write([]byte("lumeidc auth challenge v1:" + kind + ":" + channel + ":" + value))
	return hex.EncodeToString(mac.Sum(nil))
}
