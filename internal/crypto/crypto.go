// Package crypto 提供敏感字段（如 services.password_crypt）的 AES-GCM 加解密。
// 密钥复用 config.secret_key（base64 编码的 >=32 字节，与 session 签名密钥同源不同用途）。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Cryptor AES-256-GCM 加解密器。密文格式：base64(nonce(12) + ciphertext)。
type Cryptor struct{ aead cipher.AEAD }

// New 从 base64 编码的 secret_key 构造（取前 32 字节）。
func New(secretKeyB64 string) (*Cryptor, error) {
	key, err := base64.StdEncoding.DecodeString(secretKeyB64)
	if err != nil {
		return nil, fmt.Errorf("secret_key 不是有效的 base64: %w", err)
	}
	if len(key) < 32 {
		return nil, errors.New("secret_key 至少需要 32 字节")
	}
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cryptor{aead: aead}, nil
}

// Encrypt 加密明文，返回 base64 密文。空明文返回空串（无值可加密）。
func (c *Cryptor) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(c.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Decrypt 解密 Encrypt 产物。空串返回空串；密文损坏/密钥不符返回错误。
func (c *Cryptor) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码失败: %w", err)
	}
	if len(raw) < c.aead.NonceSize() {
		return "", errors.New("密文长度无效")
	}
	plain, err := c.aead.Open(nil, raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("解密失败（密钥不符或密文损坏）: %w", err)
	}
	return string(plain), nil
}

// DeriveKey 通过用途域分离从主密钥派生独立密钥，兼容旧安装迁移。
func DeriveKey(masterB64, purpose string) (string, error) {
	master, err := base64.StdEncoding.DecodeString(masterB64)
	if err != nil || len(master) < 32 {
		return "", errors.New("主密钥无效")
	}
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("lumeidc key derivation v1:"))
	mac.Write([]byte(purpose))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}
