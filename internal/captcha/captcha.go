package captcha

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"strings"
	"time"

	"lumeidc/internal/repo"
)

const (
	width      = 150
	height     = 48
	answerSize = 5
)

var alphabet = []byte("23456789ABCDEFGHJKLMNPQRSTUVWXYZ")

type Service struct {
	DB       *sql.DB
	Settings *repo.Settings
	Key      []byte
}

func New(db *sql.DB, settings *repo.Settings, key []byte) *Service {
	return &Service{DB: db, Settings: settings, Key: append([]byte(nil), key...)}
}
func (s *Service) Enabled(ctx context.Context, scene string) bool {
	if s == nil || s.DB == nil || s.Settings == nil || len(s.Key) == 0 {
		return false
	}
	v, _ := s.Settings.Get(ctx, "captcha_enabled")
	if v != "1" {
		return false
	}
	v, _ = s.Settings.Get(ctx, "captcha_"+strings.ToLower(strings.TrimSpace(scene))+"_enabled")
	return v == "1"
}
func (s *Service) Issue(ctx context.Context, scene, ip string) (string, []byte, error) {
	if !s.Enabled(ctx, scene) {
		return "", nil, errors.New("图形验证码未启用")
	}
	return s.issue(ctx, scene, ip)
}

// IssueForced 用于已开启邮箱/手机验证注册的强制人机验证，不受注册直接验证开关影响。
func (s *Service) IssueForced(ctx context.Context, scene, ip string) (string, []byte, error) {
	if s == nil || s.DB == nil || len(s.Key) == 0 {
		return "", nil, errors.New("图形验证码服务未配置")
	}
	return s.issue(ctx, scene, ip)
}

func (s *Service) issue(ctx context.Context, scene, ip string) (string, []byte, error) {
	idBytes := make([]byte, 18)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, errors.New("生成验证码失败")
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)
	answerBytes := make([]byte, answerSize)
	if _, err := rand.Read(answerBytes); err != nil {
		return "", nil, errors.New("生成验证码失败")
	}
	answer := make([]byte, answerSize)
	for i, b := range answerBytes {
		answer[i] = alphabet[int(b)%len(alphabet)]
	}
	digest := s.digest(scene, id, string(answer))
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO captcha_challenges(id,scene,answer_hmac,request_ip,expires_at) VALUES($1,$2,$3,$4,$5)`, id, strings.ToLower(scene), digest, ip, time.Now().Add(10*time.Minute)); err != nil {
		return "", nil, err
	}
	var out bytes.Buffer
	if err := png.Encode(&out, drawCaptcha(answer)); err != nil {
		return "", nil, err
	}
	return id, out.Bytes(), nil
}
func (s *Service) Verify(ctx context.Context, scene, id, answer, ip string) error {
	if !s.Enabled(ctx, scene) {
		return nil
	}
	return s.verify(ctx, scene, id, answer, ip)
}

// VerifyForced 配合 IssueForced 使用，确保验证注册发码必须先通过本地图形验证码。
func (s *Service) VerifyForced(ctx context.Context, scene, id, answer, ip string) error {
	if s == nil || s.DB == nil || len(s.Key) == 0 {
		return errors.New("图形验证码服务未配置")
	}
	return s.verify(ctx, scene, id, answer, ip)
}

func (s *Service) verify(ctx context.Context, scene, id, answer, ip string) error {
	id = strings.TrimSpace(id)
	answer = strings.ToUpper(strings.TrimSpace(answer))
	if id == "" || len(answer) != answerSize {
		return errors.New("图形验证码错误")
	}
	var saved, requestIP string
	var expires time.Time
	var attempts int
	err := s.DB.QueryRowContext(ctx, `SELECT answer_hmac,request_ip,expires_at,attempts FROM captcha_challenges WHERE id=$1 AND scene=$2 AND consumed_at IS NULL`, id, strings.ToLower(scene)).Scan(&saved, &requestIP, &expires, &attempts)
	if err != nil || attempts >= 5 || !expires.After(time.Now()) || requestIP != ip || !secureEqual(saved, s.digest(scene, id, answer)) {
		if err == nil && attempts < 5 {
			_, _ = s.DB.ExecContext(ctx, `UPDATE captcha_challenges SET attempts=attempts+1 WHERE id=$1 AND consumed_at IS NULL`, id)
		}
		return errors.New("图形验证码错误或已过期")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE captcha_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("图形验证码已使用")
	}
	return nil
}
func (s *Service) digest(scene, id, answer string) string {
	m := hmac.New(sha256.New, s.Key)
	_, _ = io.WriteString(m, "lumeidc local captcha v1:"+scene+":"+id+":"+answer)
	return fmt.Sprintf("%x", m.Sum(nil))
}
func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var d byte
	for i := range a {
		d |= a[i] ^ b[i]
	}
	return d == 0
}

var glyphs = map[byte][7]byte{
	'2': {6, 9, 1, 2, 4, 8, 15}, '3': {14, 1, 1, 6, 1, 1, 14}, '4': {2, 6, 10, 10, 15, 2, 2}, '5': {15, 8, 8, 14, 1, 1, 14}, '6': {6, 8, 8, 14, 9, 9, 6}, '7': {15, 1, 2, 4, 4, 4, 4}, '8': {6, 9, 9, 6, 9, 9, 6}, '9': {6, 9, 9, 7, 1, 1, 6},
	'A': {6, 9, 9, 15, 9, 9, 9}, 'B': {14, 9, 9, 14, 9, 9, 14}, 'C': {6, 9, 8, 8, 8, 9, 6}, 'D': {14, 9, 9, 9, 9, 9, 14}, 'E': {15, 8, 8, 14, 8, 8, 15}, 'F': {15, 8, 8, 14, 8, 8, 8}, 'G': {6, 9, 8, 11, 9, 9, 7}, 'H': {9, 9, 9, 15, 9, 9, 9}, 'J': {1, 1, 1, 1, 1, 9, 6}, 'K': {9, 10, 12, 12, 10, 10, 9}, 'L': {8, 8, 8, 8, 8, 8, 15}, 'M': {9, 15, 15, 9, 9, 9, 9}, 'N': {9, 13, 15, 11, 9, 9, 9}, 'P': {14, 9, 9, 14, 8, 8, 8}, 'Q': {6, 9, 9, 9, 10, 5, 7}, 'R': {14, 9, 9, 14, 10, 9, 9}, 'S': {7, 8, 8, 6, 1, 1, 14}, 'T': {15, 2, 2, 2, 2, 2, 2}, 'U': {9, 9, 9, 9, 9, 9, 6}, 'V': {9, 9, 9, 9, 9, 9, 6}, 'W': {9, 9, 9, 15, 15, 15, 6}, 'X': {9, 9, 6, 6, 6, 9, 9}, 'Y': {9, 9, 6, 6, 2, 2, 2}, 'Z': {15, 1, 2, 4, 8, 8, 15}}

func drawCaptcha(answer []byte) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{245, 248, 252, 255}}, image.Point{}, draw.Src)
	for i := 0; i < 14; i++ {
		x := (i*37 + int(answer[i%len(answer)])) % width
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{210, 220, 235, 255})
		}
	}
	for i, ch := range answer {
		glyph := glyphs[ch]
		x0 := 12 + i*27
		for y, row := range glyph {
			for x := 0; x < 4; x++ {
				if row&(1<<uint(3-x)) != 0 {
					for dy := 0; dy < 4; dy++ {
						for dx := 0; dx < 4; dx++ {
							img.Set(x0+x*4+dx, 8+y*4+dy, color.RGBA{35, 74, 122, 255})
						}
					}
				}
			}
		}
	}
	return img
}
