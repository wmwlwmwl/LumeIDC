package config

import (
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

//go:embed config.example.yaml
var Example []byte

type Config struct {
	Listen          string `yaml:"listen"`
	DBDSN           string `yaml:"db_dsn"`
	SecretKey       string `yaml:"secret_key"` // session + password crypt key, base64(32 bytes)
	PIIKey          string `yaml:"pii_key"`    // 实名资料专用密钥，base64(32 bytes)
	PrivateDataDir  string `yaml:"private_data_dir"`
	BaseURL         string `yaml:"base_url"`
	AllowInsecureDB bool   `yaml:"allow_insecure_db"` // 显式放行 sslmode=disable 的远程库（默认拒绝）
	InstallLock     bool   `yaml:"-"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == "config.yaml" {
			return nil, ErrNotInstalled
		}
		return nil, err
	}
	c := &Config{}
	if err := yamlUnmarshalStrict(b, c); err != nil {
		return nil, fmt.Errorf("配置文件解析失败: %w", err)
	}
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.PrivateDataDir == "" {
		c.PrivateDataDir = "data/private"
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate 校验必填字段和安全性。
func (c *Config) Validate() error {
	if c.DBDSN == "" {
		return fmt.Errorf("db_dsn 不能为空")
	}
	if c.SecretKey == "" {
		return fmt.Errorf("secret_key 不能为空")
	}
	if len(c.SecretKey) < 16 {
		return fmt.Errorf("secret_key 长度不能少于 16 字符")
	}
	var err error
	if c.PIIKey == "" {
		// 旧安装没有独立 pii_key 时用域分离派生值兼容启动；新安装由安装器生成独立密钥。
		c.PIIKey, err = deriveLegacyPIIKey(c.SecretKey)
		if err != nil {
			return err
		}
	}
	key, err := base64.StdEncoding.DecodeString(c.PIIKey)
	if err != nil || len(key) < 32 {
		return fmt.Errorf("pii_key 必须是 base64 编码的至少 32 字节")
	}
	// 生产环境拒绝不安全的数据库连接（allow_insecure_db 显式开启时放行）
	if !c.AllowInsecureDB && strings.Contains(c.DBDSN, "sslmode=disable") && !strings.Contains(c.DBDSN, "localhost") && !strings.Contains(c.DBDSN, "127.0.0.1") {
		return fmt.Errorf("生产环境请使用 SSL 数据库连接（移除 sslmode=disable）；如确需关闭，请在配置中显式设置 allow_insecure_db: true")
	}
	return nil
}

func deriveLegacyPIIKey(secret string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(secret)
	if err != nil || len(key) < 32 {
		return "", fmt.Errorf("secret_key 无效，无法派生实名资料密钥")
	}
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte("lumeidc identity pii v1"))
	return base64.StdEncoding.EncodeToString(m.Sum(nil)), nil
}

var ErrNotInstalled = errors.New("lumeidc: 未安装（缺少 config.yaml）")
