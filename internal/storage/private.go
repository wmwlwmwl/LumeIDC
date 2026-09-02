package storage

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxIdentityPhotoBytes = 5 << 20

// PrivateFiles 将实名照片保存到 web root 外的私有目录。
type PrivateFiles struct {
	Root string
}

func (s *PrivateFiles) SavePhoto(header *multipart.FileHeader) (string, error) {
	if s == nil || s.Root == "" {
		return "", errors.New("私有存储未配置")
	}
	if header == nil || header.Size <= 0 || header.Size > maxIdentityPhotoBytes {
		return "", errors.New("照片大小必须在 1 字节至 5 MB 之间")
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return "", fmt.Errorf("创建私有目录失败: %w", err)
	}
	f, err := header.Open()
	if err != nil {
		return "", errors.New("读取照片失败")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxIdentityPhotoBytes+1))
	if err != nil || len(data) == 0 {
		return "", errors.New("读取照片失败")
	}
	if len(data) > maxIdentityPhotoBytes {
		return "", errors.New("照片不能超过 5 MB")
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return "", errors.New("只支持 JPG 或 PNG 照片")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") || cfg.Width < 100 || cfg.Height < 100 || cfg.Width > 8000 || cfg.Height > 8000 {
		return "", errors.New("照片格式或尺寸无效")
	}
	var nameBytes [24]byte
	if _, err := rand.Read(nameBytes[:]); err != nil {
		return "", errors.New("生成照片标识失败")
	}
	ext := ".jpg"
	if format == "png" {
		ext = ".png"
	}
	name := fmt.Sprintf("%x%s", nameBytes, ext)
	path := filepath.Join(s.Root, name)
	if filepath.Base(path) != name {
		return "", errors.New("照片路径无效")
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", errors.New("保存照片失败")
	}
	return name, nil
}

func (s *PrivateFiles) Delete(ref string) error {
	path, err := s.path(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *PrivateFiles) Open(ref string) (*os.File, error) {
	path, err := s.path(ref)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *PrivateFiles) path(ref string) (string, error) {
	if s == nil || s.Root == "" || ref == "" || filepath.Base(ref) != ref || strings.ContainsAny(ref, `/\\`) {
		return "", errors.New("照片引用无效")
	}
	return filepath.Join(s.Root, ref), nil
}
