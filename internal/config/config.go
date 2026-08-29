package config

import (
	_ "embed"
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
	// 生产环境拒绝不安全的数据库连接（allow_insecure_db 显式开启时放行）
	if !c.AllowInsecureDB && strings.Contains(c.DBDSN, "sslmode=disable") && !strings.Contains(c.DBDSN, "localhost") && !strings.Contains(c.DBDSN, "127.0.0.1") {
		return fmt.Errorf("生产环境请使用 SSL 数据库连接（移除 sslmode=disable）；如确需关闭，请在配置中显式设置 allow_insecure_db: true")
	}
	return nil
}

var ErrNotInstalled = errors.New("lumeidc: 未安装（缺少 config.yaml）")
