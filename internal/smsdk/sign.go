package smsdk

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CanonicalQuery 规范化查询串（阿里云 ACS3 签名用）：键排序 + RFC3986 编码。
func CanonicalQuery(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(PercentEncode(k))
		b.WriteByte('=')
		b.WriteString(PercentEncode(values[k]))
	}
	return b.String()
}

// PercentEncode RFC3986（QueryEscape 的 +/%7E 修正）。
func PercentEncode(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(v), "+", "%20"), "%7E", "~")
}

// HMACSHA256 HMAC-SHA256 摘要。
func HMACSHA256(key, value []byte) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(value)
	return m.Sum(nil)
}

// SHA256Hex SHA256 十六进制。
func SHA256Hex(value []byte) string {
	h := sha256.Sum256(value)
	return hex.EncodeToString(h[:])
}

// RandomHex 随机十六进制串（size 字节 → 2*size 字符）。
func RandomHex(size int) string {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// MD5Hex MD5 十六进制。
func MD5Hex(v string) string { h := md5.Sum([]byte(v)); return hex.EncodeToString(h[:]) }

// TC3Signature 腾讯云 TC3-HMAC-SHA256 签名（短信 API）。
func TC3Signature(secretID, secretKey, host, action string, body []byte, now time.Time) string {
	date := now.UTC().Format("2006-01-02")
	canonical := "POST\n/\n\ncontent-type:application/json; charset=utf-8\nhost:" + host + "\n\ncontent-type;host\n" + SHA256Hex(body)
	credential := date + "/sms/tc3_request"
	text := "TC3-HMAC-SHA256\n" + strconv.FormatInt(now.Unix(), 10) + "\n" + credential + "\n" + SHA256Hex([]byte(canonical))
	key := HMACSHA256([]byte("TC3"+secretKey), []byte(date))
	key = HMACSHA256(key, []byte("sms"))
	key = HMACSHA256(key, []byte("tc3_request"))
	return fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=content-type;host, Signature=%s", secretID, credential, hex.EncodeToString(HMACSHA256(key, []byte(text))))
}
