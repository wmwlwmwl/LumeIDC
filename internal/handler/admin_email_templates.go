package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"lumeidc/internal/middleware"
	"lumeidc/internal/service"
)

const emailTemplateRequestLimit = 2 << 20

func emailTemplateError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"ok": 0, "msg": msg})
}

func decodeEmailTemplateJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		emailTemplateError(w, http.StatusUnsupportedMediaType, "请使用 JSON 格式提交")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, emailTemplateRequestLimit)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			emailTemplateError(w, http.StatusRequestEntityTooLarge, "请求内容过大，请缩短邮件正文")
		} else {
			emailTemplateError(w, http.StatusBadRequest, "读取请求失败，请重试")
		}
		return false
	}
	if !utf8.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		emailTemplateError(w, http.StatusBadRequest, "请求编码或格式无效，请使用有效的 UTF-8 JSON 对象")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		emailTemplateError(w, http.StatusBadRequest, "请求格式或字段无效，请检查后重试")
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		emailTemplateError(w, http.StatusBadRequest, "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}

func (a *Admin) requireEmailTemplates(w http.ResponseWriter, r *http.Request) bool {
	if !adminRequire(w, r) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if a.Notifier == nil {
		emailTemplateError(w, http.StatusServiceUnavailable, "邮件服务暂不可用，请稍后重试")
		return false
	}
	return true
}

func (a *Admin) adminEmailTemplates(w http.ResponseWriter, r *http.Request) {
	if !a.requireEmailTemplates(w, r) {
		return
	}
	list, err := a.Notifier.ListEmailTemplates(r.Context())
	if err != nil {
		emailTemplateError(w, http.StatusInternalServerError, "读取邮件模板失败，请稍后重试")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "list": list, "email_enabled": a.Notifier.EmailForwardEnabled(r.Context())})
}

// adminEmailTemplateMaster POST /admin/email-templates/master — 业务邮件总开关（验证码、短信不受影响）。
func (a *Admin) adminEmailTemplateMaster(w http.ResponseWriter, r *http.Request) {
	if !a.requireEmailTemplates(w, r) {
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeEmailTemplateJSON(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		emailTemplateError(w, http.StatusBadRequest, "请提供有效的邮件通知总开关状态")
		return
	}
	if err := a.Notifier.SetEmailForwardEnabled(r.Context(), *input.Enabled); err != nil {
		emailTemplateError(w, http.StatusInternalServerError, "保存邮件通知总开关失败，请稍后重试")
		return
	}
	if a.AdminLog != nil {
		state := "关闭"
		if *input.Enabled {
			state = "开启"
		}
		a.recordEmailTemplateChange(r, "email_template_master_updated", state)
	}
	msg := "邮件通知总开关已关闭，业务邮件不再发送"
	if *input.Enabled {
		msg = "邮件通知总开关已开启"
	}
	writeJSON(w, map[string]any{"ok": 1, "msg": msg})
}

type emailTemplateInput struct {
	Code    string `json:"code"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Enabled *bool  `json:"enabled"`
	To      string `json:"to,omitempty"`
}

func (a *Admin) readEmailTemplateDraft(w http.ResponseWriter, r *http.Request, test bool) (service.EmailTemplateDraft, string, bool) {
	var input emailTemplateInput
	if !decodeEmailTemplateJSON(w, r, &input) {
		return service.EmailTemplateDraft{}, "", false
	}
	if input.Code == "" || len(input.Code) > 100 || input.Enabled == nil ||
		strings.TrimSpace(input.Subject) == "" || len(input.Subject) > 512 || strings.ContainsAny(input.Subject, "\r\n\x00") ||
		strings.TrimSpace(input.Body) == "" || len(input.Body) > 256<<10 || strings.ContainsRune(input.Body, '\x00') ||
		(!test && input.To != "") || (test && (strings.TrimSpace(input.To) == "" || len(input.To) > 320)) {
		emailTemplateError(w, http.StatusBadRequest, "请检查模板编码、启用状态、标题（最多512字节）、正文（最多256KB）和收件邮箱")
		return service.EmailTemplateDraft{}, "", false
	}
	draft := service.EmailTemplateDraft{Code: input.Code, Subject: input.Subject, Body: input.Body, Enabled: *input.Enabled}
	if !a.validateEmailTemplateCode(w, r, draft.Code, !draft.Enabled) {
		return service.EmailTemplateDraft{}, "", false
	}
	return draft, strings.TrimSpace(input.To), true
}

func (a *Admin) validateEmailTemplateCode(w http.ResponseWriter, r *http.Request, code string, disabling bool) bool {
	list, err := a.Notifier.ListEmailTemplates(r.Context())
	if err != nil {
		emailTemplateError(w, http.StatusInternalServerError, "读取邮件模板失败，请稍后重试")
		return false
	}
	for _, item := range list {
		if item.Code == code {
			if item.Required && disabling {
				emailTemplateError(w, http.StatusBadRequest, "验证码等必要邮件模板不能停用")
				return false
			}
			return true
		}
	}
	emailTemplateError(w, http.StatusBadRequest, "请选择已有业务邮件模板，不支持新增模板")
	return false
}

func (a *Admin) adminEmailTemplateSave(w http.ResponseWriter, r *http.Request) {
	if !a.requireEmailTemplates(w, r) {
		return
	}
	draft, _, ok := a.readEmailTemplateDraft(w, r, false)
	if !ok {
		return
	}
	if err := a.Notifier.SaveEmailTemplate(r.Context(), draft); err != nil {
		emailTemplateError(w, http.StatusBadRequest, "保存失败，请检查模板变量和格式，或稍后重试；当前草稿未被清除")
		return
	}
	a.recordEmailTemplateChange(r, "email_template_saved", draft.Code)
	writeJSON(w, map[string]any{"ok": 1, "msg": "邮件模板已保存"})
}

func (a *Admin) adminEmailTemplateReset(w http.ResponseWriter, r *http.Request) {
	if !a.requireEmailTemplates(w, r) {
		return
	}
	var input struct {
		Code    string `json:"code"`
		Confirm bool   `json:"confirm"`
	}
	if !decodeEmailTemplateJSON(w, r, &input) {
		return
	}
	if !input.Confirm || input.Code == "" || len(input.Code) > 100 {
		emailTemplateError(w, http.StatusBadRequest, "请明确确认恢复该模板的默认内容和启用状态")
		return
	}
	if !a.validateEmailTemplateCode(w, r, input.Code, false) {
		return
	}
	if err := a.Notifier.ResetEmailTemplate(r.Context(), input.Code); err != nil {
		emailTemplateError(w, http.StatusInternalServerError, "恢复默认失败，请稍后重试")
		return
	}
	a.recordEmailTemplateChange(r, "email_template_reset", input.Code)
	writeJSON(w, map[string]any{"ok": 1, "msg": "邮件模板已恢复默认"})
}

func (a *Admin) adminEmailTemplatePreview(w http.ResponseWriter, r *http.Request) {
	if !a.requireEmailTemplates(w, r) {
		return
	}
	draft, _, ok := a.readEmailTemplateDraft(w, r, false)
	if !ok {
		return
	}
	preview, err := a.Notifier.PreviewEmailTemplate(r.Context(), draft)
	if err != nil {
		emailTemplateError(w, http.StatusBadRequest, "预览失败，请检查模板格式及可用变量后重试")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "preview": preview})
}

func (a *Admin) adminEmailTemplateTest(w http.ResponseWriter, r *http.Request) {
	if !a.requireEmailTemplates(w, r) {
		return
	}
	draft, to, ok := a.readEmailTemplateDraft(w, r, true)
	if !ok || !a.allowEmailTest(w, r) {
		return
	}
	if err := a.Notifier.TestEmailTemplate(r.Context(), to, draft); err != nil {
		emailTemplateError(w, http.StatusBadRequest, "测试发送失败，请检查单个收件邮箱、模板变量及邮件通道配置后重试")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "msg": "当前草稿测试邮件已发送，未保存模板"})
}

// ponytail: 复用登录计数实现每管理员前5次、之后每15分钟1次的测试额度；单实例锁保证检查与计数串行。
// 多实例若需严格原子额度，应将检查与计数合为共享存储事务；此锁不覆盖实际发信。
func (a *Admin) allowEmailTest(w http.ResponseWriter, r *http.Request) bool {
	a.emailTestMu.Lock()
	defer a.emailTestMu.Unlock()
	if a.Lockout == nil {
		emailTemplateError(w, http.StatusServiceUnavailable, "测试发送限流服务暂不可用，请稍后重试")
		return false
	}
	key := "admin-email-test:" + strconv.FormatInt(middleware.FromSession(r.Context()).UserID, 10)
	locked, err := a.Lockout.Locked(r.Context(), key)
	if err != nil {
		emailTemplateError(w, http.StatusServiceUnavailable, "读取测试发送额度失败，请稍后重试")
		return false
	}
	if locked {
		w.Header().Set("Retry-After", "900")
		emailTemplateError(w, http.StatusTooManyRequests, "测试发送过于频繁，请15分钟后重试")
		return false
	}
	if err := a.Lockout.Fail(r.Context(), key); err != nil {
		emailTemplateError(w, http.StatusServiceUnavailable, "记录测试发送额度失败，本次未发送，请稍后重试")
		return false
	}
	return true
}

func (a *Admin) recordEmailTemplateChange(r *http.Request, action, code string) {
	if a.AdminLog != nil {
		a.AdminLog.Record(middleware.FromSession(r.Context()).UserID, action, "email_template", 0, code, requestIP(r))
	}
}
