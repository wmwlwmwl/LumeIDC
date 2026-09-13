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
const maxTicketAttachmentBytes = 10 << 20

// PrivateFiles 将实名照片保存到 web root 外的私有目录。
type PrivateFiles struct {
	Root string
}

// readUpload 读入上传文件并嗅探 MIME：大小校验 → 读取（限额）→ 内容类型探测。
// kind/limitText 仅用于错误文案（如 "附件"/"10 MB"）。
func readUpload(header *multipart.FileHeader, maxBytes int64, kind, limitText string) ([]byte, string, error) {
	if header == nil || header.Size <= 0 || header.Size > maxBytes {
		return nil, "", fmt.Errorf("%s大小必须在 1 字节至 %s 之间", kind, limitText)
	}
	f, err := header.Open()
	if err != nil {
		return nil, "", fmt.Errorf("读取%s失败", kind)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(data) == 0 {
		return nil, "", fmt.Errorf("读取%s失败", kind)
	}
	if len(data) > int(maxBytes) {
		return nil, "", fmt.Errorf("%s不能超过 %s", kind, limitText)
	}
	return data, http.DetectContentType(data), nil
}

// writePrivate 随机名已生成的前提下写入私有目录（Base 校验 + 0600）。
func (s *PrivateFiles) writePrivate(name string, data []byte, kind string) error {
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return fmt.Errorf("创建私有目录失败: %w", err)
	}
	path := filepath.Join(s.Root, name)
	if filepath.Base(path) != name {
		return fmt.Errorf("%s路径无效", kind)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("保存%s失败", kind)
	}
	return nil
}

// SaveAttachment stores a ticket attachment outside the web root.
func (s *PrivateFiles) SaveAttachment(header *multipart.FileHeader) (string, string, string, int64, error) {
	if s == nil || s.Root == "" {
		return "", "", "", 0, errors.New("私有存储未配置")
	}
	data, mimeType, err := readUpload(header, maxTicketAttachmentBytes, "附件", "10 MB")
	if err != nil {
		return "", "", "", 0, err
	}
	allowed := map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "application/pdf": true, "text/plain; charset=utf-8": true, "application/zip": true}
	if !allowed[mimeType] {
		return "", "", "", 0, errors.New("仅支持 JPG、PNG、GIF、PDF、TXT 或 ZIP 附件")
	}
	var nameBytes [24]byte
	if _, err := rand.Read(nameBytes[:]); err != nil {
		return "", "", "", 0, err
	}
	name := fmt.Sprintf("ticket-%x", nameBytes)
	if err := s.writePrivate(name, data, "附件"); err != nil {
		return "", "", "", 0, err
	}
	return name, filepath.Base(header.Filename), mimeType, int64(len(data)), nil
}

func (s *PrivateFiles) SavePhoto(header *multipart.FileHeader) (string, error) {
	if s == nil || s.Root == "" {
		return "", errors.New("私有存储未配置")
	}
	data, contentType, err := readUpload(header, maxIdentityPhotoBytes, "照片", "5 MB")
	if err != nil {
		return "", err
	}
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
	if err := s.writePrivate(name, data, "照片"); err != nil {
		return "", err
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
