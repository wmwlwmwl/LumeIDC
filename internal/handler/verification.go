package handler

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

type VerificationHandler struct {
	Identity   *service.Identity
	Users      *repo.Users
	Sessions   *middleware.Store
	AdminLog   *repo.AdminLog
	Challenges *service.AuthChallengeService
	*Deps
}

func (h *VerificationHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /user/profile", h.profile)
	mux.HandleFunc("POST /user/profile", h.updateProfile)
	mux.HandleFunc("POST /user/profile/email/send", h.sendEmailChange)
	mux.HandleFunc("POST /user/profile/email/confirm", h.confirmEmailChange)
	mux.HandleFunc("POST /user/profile/phone/send", h.sendPhone)
	mux.HandleFunc("POST /user/profile/phone/confirm", h.confirmPhone)
	mux.HandleFunc("GET /user/verification", h.verification)
	mux.HandleFunc("POST /user/verification", h.submitVerification)
	mux.HandleFunc("POST /user/verification/plugin/start", h.startPlugin)
	mux.HandleFunc("POST /user/verification/plugin/poll", h.pollPlugin)
}

func (h *VerificationHandler) profile(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if h.Identity == nil || h.Identity.Store == nil || h.Users == nil {
		http.Error(w, "实名服务未配置", http.StatusServiceUnavailable)
		return
	}
	profile, err := h.Users.Profile(r.Context(), userID)
	if err != nil {
		http.Error(w, "读取账户信息失败", http.StatusInternalServerError)
		return
	}
	phone, verified, err := h.Identity.Store.UserPhone(r.Context(), userID)
	if err != nil {
		http.Error(w, "读取账户信息失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"ok": 1, "email": profile.Email, "email_verified": profile.Verified, "name": profile.Name,
		"phone": phone, "phone_masked": service.MaskPhone(phone),
		"phone_verified": verified, "has_phone": phone != "",
	})
}

func (h *VerificationHandler) updateProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok || h.Users == nil {
		return
	}
	vals := jsonVals(r)
	value := func(key string) string {
		if vals != nil {
			return vals[key]
		}
		return r.PostFormValue(key)
	}
	name := strings.TrimSpace(value("name"))
	email, err := repo.NormalizeEmail(value("email"))
	if err != nil {
		jsonStatus(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if len(name) > 80 {
		jsonStatus(w, r, http.StatusBadRequest, "名称不能超过 80 个字符")
		return
	}
	profile, err := h.Users.Profile(r.Context(), userID)
	if err != nil {
		jsonStatus(w, r, 500, "读取账户信息失败")
		return
	}
	if email != profile.Email {
		jsonStatus(w, r, http.StatusBadRequest, "修改邮箱请先获取并验证新邮箱验证码")
		return
	}
	if err := h.Users.UpdateProfile(r.Context(), userID, name, email); err != nil {
		jsonStatus(w, r, 500, "保存资料失败")
		return
	}
	if h.AdminLog != nil {
		h.AdminLog.Record(0, "profile_updated", "user", userID, "email_changed=false", requestIP(r))
	}
	writeJSON(w, map[string]any{"ok": 1, "name": name, "email": email, "email_verified": profile.Verified, "msg": "资料已保存"})
}

func (h *VerificationHandler) sendEmailChange(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok || h.Users == nil || h.Challenges == nil {
		return
	}
	vals := jsonVals(r)
	value := func(key string) string {
		if vals != nil {
			return vals[key]
		}
		return r.PostFormValue(key)
	}
	email, err := repo.NormalizeEmail(value("email"))
	if err != nil {
		jsonStatus(w, r, 400, err.Error())
		return
	}
	profile, err := h.Users.Profile(r.Context(), userID)
	if err != nil {
		jsonStatus(w, r, 500, "读取账户信息失败")
		return
	}
	if email == profile.Email {
		jsonStatus(w, r, 400, "新邮箱不能与当前邮箱相同")
		return
	}
	if other, _, findErr := h.Users.ByEmail(r.Context(), email); findErr == nil && other.ID != userID {
		jsonStatus(w, r, 400, "该邮箱已被使用")
		return
	}
	hash, err := h.Users.PasswordHash(r.Context(), userID)
	if err != nil || !h.Users.VerifyPassword(hash, value("current_password")) {
		jsonStatus(w, r, 400, "当前密码错误")
		return
	}
	if err := h.Challenges.Issue(r.Context(), "email", "profile_email", email, requestIP(r)); err != nil {
		jsonStatus(w, r, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "msg": "验证码已发送，请查收新邮箱"})
}

func (h *VerificationHandler) confirmEmailChange(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok || h.Users == nil || h.Challenges == nil {
		return
	}
	vals := jsonVals(r)
	value := func(key string) string {
		if vals != nil {
			return vals[key]
		}
		return r.PostFormValue(key)
	}
	email, err := repo.NormalizeEmail(value("email"))
	if err != nil {
		jsonStatus(w, r, 400, err.Error())
		return
	}
	if err := h.Challenges.Verify(r.Context(), "email", "profile_email", email, strings.TrimSpace(value("code"))); err != nil {
		jsonStatus(w, r, 400, "验证码错误或已过期")
		return
	}
	if other, _, findErr := h.Users.ByEmail(r.Context(), email); findErr == nil && other.ID != userID {
		jsonStatus(w, r, 400, "该邮箱已被使用")
		return
	}
	profile, err := h.Users.Profile(r.Context(), userID)
	if err != nil {
		jsonStatus(w, r, 500, "读取账户信息失败")
		return
	}
	name := strings.TrimSpace(value("name"))
	if name == "" {
		name = profile.Name
	}
	if len(name) > 80 {
		jsonStatus(w, r, 400, "名称不能超过 80 个字符")
		return
	}
	if err := h.Users.UpdateProfile(r.Context(), userID, name, email); err != nil {
		jsonStatus(w, r, 500, "保存邮箱失败")
		return
	}
	if h.AdminLog != nil {
		h.AdminLog.Record(0, "email_changed", "user", userID, "", requestIP(r))
	}
	writeJSON(w, map[string]any{"ok": 1, "name": name, "email": email, "email_verified": true, "msg": "邮箱已验证并更新"})
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (h *VerificationHandler) sendPhone(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	fail := func(status int, msg string) {
		if wantsJSON(r) {
			jsonStatus(w, r, status, msg)
			return
		}
		http.Redirect(w, r, "/user/profile?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	if h.Identity == nil || h.Identity.Store == nil || h.Users == nil {
		fail(http.StatusServiceUnavailable, "手机号服务未配置")
		return
	}
	phone, _, err := h.Identity.Store.UserPhone(r.Context(), userID)
	if err != nil {
		fail(http.StatusInternalServerError, "读取账户信息失败")
		return
	}
	purpose := "bind"
	if phone != "" {
		purpose = "change"
		hash, hashErr := h.Users.PasswordHash(r.Context(), userID)
		if hashErr != nil || !h.Users.VerifyPassword(hash, fv("current_password")) {
			fail(http.StatusBadRequest, "当前密码错误")
			return
		}
	}
	if err := h.Identity.RequestPhoneCode(r.Context(), userID, fv("phone"), purpose, requestIP(r)); err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	h.AdminLog.Record(0, "phone_otp_requested", "user", userID, "purpose="+purpose, requestIP(r))
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "验证码已发送，请查收短信"})
		return
	}
	http.Redirect(w, r, "/user/profile?ok="+url.QueryEscape("验证码已发送，请查收短信"), http.StatusSeeOther)
}

func (h *VerificationHandler) confirmPhone(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	fail := func(status int, msg string) {
		if wantsJSON(r) {
			jsonStatus(w, r, status, msg)
			return
		}
		http.Redirect(w, r, "/user/profile?err="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	if h.Identity == nil || h.Identity.Store == nil || h.Users == nil {
		fail(http.StatusServiceUnavailable, "手机号服务未配置")
		return
	}
	phone, _, err := h.Identity.Store.UserPhone(r.Context(), userID)
	if err != nil {
		fail(http.StatusInternalServerError, "读取账户信息失败")
		return
	}
	purpose := "bind"
	if phone != "" {
		purpose = "change"
	}
	if err := h.Identity.ConfirmPhoneCode(r.Context(), userID, purpose, fv("code")); err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	h.AdminLog.Record(0, "phone_verified", "user", userID, "purpose="+purpose, requestIP(r))
	if purpose == "change" {
		h.AdminLog.Record(0, "phone_changed", "user", userID, "", requestIP(r))
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "手机号验证成功"})
		return
	}
	http.Redirect(w, r, "/user/profile?ok="+url.QueryEscape("手机号验证成功"), http.StatusSeeOther)
}

func (h *VerificationHandler) verification(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if h.Identity == nil || h.Identity.Store == nil {
		http.Error(w, "实名服务未配置", http.StatusServiceUnavailable)
		return
	}
	v, err := h.Identity.Current(r.Context(), userID)
	if err != nil {
		http.Error(w, "读取实名状态失败", http.StatusInternalServerError)
		return
	}
	auto, autoErr := h.Identity.CurrentAutomatic(r.Context(), userID)
	if autoErr != nil {
		http.Error(w, "读取自动实名状态失败", http.StatusInternalServerError)
		return
	}
	status := ""
	reason := ""
	maskedID := ""
	if v != nil {
		status = v.Status
		reason = v.RejectionReason
		if h.Identity.PII != nil {
			if plain, derr := h.Identity.PII.Decrypt(v.IdentityNumberCiphertext); derr == nil {
				maskedID = service.MaskIdentityNumber(plain)
			}
		} else {
			maskedID = "已提交"
		}
	}
	manualEnabled := true
	pluginProvider := ""
	if h.Identity.Settings != nil {
		if enabled, err := h.Identity.Settings.Get(r.Context(), "manual_identity_enabled"); err == nil && enabled == "0" {
			manualEnabled = false
		}
		provider, _ := h.Identity.Settings.Get(r.Context(), "verification_provider")
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider != "" && provider != "manual" {
			pluginProvider = provider
		}
	}
	out := map[string]any{
		"ok":              1,
		"manual_enabled":  manualEnabled,
		"status":          status,
		"status_text":     service.StatusText(status),
		"reason":          reason,
		"masked_id":       maskedID,
		"can_submit":      status == "" || status == "rejected",
		"plugin_provider": pluginProvider,
	}
	if auto != nil {
		out["automatic_id"] = auto.ID
		out["automatic_status"] = auto.Status
		out["automatic_status_text"] = service.StatusText(auto.Status)
		out["automatic_url"] = auto.ProviderURL
	}
	writeJSON(w, out)
}

func (h *VerificationHandler) submitVerification(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if h.Identity == nil || h.Identity.Store == nil {
		http.Error(w, "人工实名服务未配置", http.StatusServiceUnavailable)
		return
	}
	// 两张照片均限制在 5 MB；额外预留 multipart 边界，不把超大请求交给解析器。
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		http.Redirect(w, r, "/user/verification?err="+url.QueryEscape("上传内容过大或格式无效"), http.StatusSeeOther)
		return
	}
	front, frontHeader, err := r.FormFile("front")
	if err != nil {
		http.Redirect(w, r, "/user/verification?err="+url.QueryEscape("请上传身份证正面照片"), http.StatusSeeOther)
		return
	}
	defer front.Close()
	back, backHeader, err := r.FormFile("back")
	if err != nil {
		http.Redirect(w, r, "/user/verification?err="+url.QueryEscape("请上传身份证背面照片"), http.StatusSeeOther)
		return
	}
	defer back.Close()
	if err := h.Identity.SubmitForm(r.Context(), userID, service.RealNameForm{
		LegalName: r.FormValue("legal_name"), IdentityNumber: r.FormValue("identity_number"),
		Front: frontHeader, Back: backHeader,
	}); err != nil {
		msg := err.Error()
		if errors.Is(err, repo.ErrVerificationBusy) {
			msg = "已有实名申请正在审核"
		}
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": msg})
			return
		}
		http.Redirect(w, r, "/user/verification?err="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	h.AdminLog.Record(0, "real_name_submitted", "user", userID, "source=manual", requestIP(r))
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "实名资料已提交，等待人工审核"})
		return
	}
	http.Redirect(w, r, "/user/verification?ok="+url.QueryEscape("实名资料已提交，等待人工审核"), http.StatusSeeOther)
}

func (h *VerificationHandler) startPlugin(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if h.Identity == nil {
		http.Error(w, "实名插件服务未配置", http.StatusServiceUnavailable)
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	provider := strings.TrimSpace(fv("provider"))
	callback := siteBaseURL(r.Context(), h.Settings, r) + "/user/verification" // 站点地址优先，否则按请求推断
	id, urlValue, err := h.Identity.StartProvider(r.Context(), userID, provider, service.RealNameForm{LegalName: fv("legal_name"), IdentityNumber: fv("identity_number")}, callback)
	if err != nil {
		http.Error(w, "启动实名认证失败", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "submission_id": id, "url": urlValue})
}

func (h *VerificationHandler) pollPlugin(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	vals := jsonVals(r)
	submissionRaw := ""
	if vals != nil {
		submissionRaw = vals["submission_id"]
	} else {
		submissionRaw = r.PostFormValue("submission_id")
	}
	id, err := strconv.ParseInt(submissionRaw, 10, 64)
	if err != nil || id <= 0 || h.Identity == nil {
		http.Error(w, "实名任务无效", http.StatusBadRequest)
		return
	}
	if err := h.Identity.PollProvider(r.Context(), userID, id); err != nil {
		http.Error(w, "查询自动实名状态失败", http.StatusBadGateway)
		return
	}
	auto, err := h.Identity.Store.AutomaticAttempt(r.Context(), userID, id)
	if err != nil || auto == nil {
		http.Error(w, "实名任务不存在", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "status": auto.Status, "message": auto.FailureMessage})
}

type AdminVerification struct {
	Identity *service.Identity
	Users    *repo.Users
	AdminLog *repo.AdminLog
	*Deps
}

func (h *AdminVerification) require(w http.ResponseWriter, r *http.Request) (*middleware.Session, bool) {
	sess := middleware.FromSession(r.Context())
	if sess == nil || !sess.IsAdmin {
		if wantsJSON(r) {
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, map[string]any{"ok": 0, "msg": "登录已过期，请重新登录"})
			return nil, false
		}
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return nil, false
	}
	return sess, true
}

func (h *AdminVerification) List(w http.ResponseWriter, r *http.Request) {
	h.list(w, r)
}

func (h *AdminVerification) Detail(w http.ResponseWriter, r *http.Request) {
	h.detail(w, r)
}

func (h *AdminVerification) Approve(w http.ResponseWriter, r *http.Request) {
	h.approve(w, r)
}

func (h *AdminVerification) Reject(w http.ResponseWriter, r *http.Request) {
	h.reject(w, r)
}

func (h *AdminVerification) Photo(w http.ResponseWriter, r *http.Request) {
	h.photo(w, r)
}

func (h *AdminVerification) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.require(w, r); !ok {
		return
	}
	rows, err := h.Identity.Store.PendingList(r.Context(), 100)
	if err != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "查询实名申请失败")
		return
	}
	for i := range rows {
		rows[i].Phone = service.MaskPhone(rows[i].Phone)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		out = append(out, map[string]any{
			"id": v.ID, "user_id": v.UserID, "email": v.Email, "phone": v.Phone,
			"status": v.Status, "submitted_at": v.SubmittedAt.Format("2006-01-02 15:04"),
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

func (h *AdminVerification) detail(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.require(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v, name, identityNumber, err := h.Identity.AdminSubmission(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.AdminLog.Record(sess.UserID, "real_name_viewed", "real_name_submission", id, "admin_detail", requestIP(r))
	front := "/admin/verifications/" + strconv.FormatInt(v.ID, 10) + "/photo/front"
	back := "/admin/verifications/" + strconv.FormatInt(v.ID, 10) + "/photo/back"
	// 身份证号给管理员看完整的：审核就是要拿它跟证件照、姓名逐位核对，
	// 脱敏等于没法审（脱敏只用于用户自己看的那一页）。本次查看已在上面记审计日志。
	writeJSON(w, map[string]any{
		"ok": 1,
		"id": v.ID, "user_id": v.UserID, "email": v.Email, "phone": service.MaskPhone(v.Phone),
		"status": service.StatusText(v.Status), "raw_status": v.Status,
		"name": name, "identity_number": identityNumber,
		"submitted_at": v.SubmittedAt.Format("2006-01-02 15:04"), "reason": v.RejectionReason,
		"front_url": front, "back_url": back,
	})
}

func (h *AdminVerification) approve(w http.ResponseWriter, r *http.Request) {
	h.review(w, r, true)
}

func (h *AdminVerification) reject(w http.ResponseWriter, r *http.Request) {
	h.review(w, r, false)
}

func (h *AdminVerification) review(w http.ResponseWriter, r *http.Request, approve bool) {
	sess, ok := h.require(w, r)
	if !ok {
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	if vals == nil {
		if tok := fv("_csrf"); tok == "" || !checkCSRF(r, tok) {
			middleware.RedirectToLogin(w, r, "页面已过期，请重新登录后重试")
			return
		}
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	reason := strings.TrimSpace(fv("reason"))
	err = h.Identity.Review(r.Context(), id, sess.UserID, approve, reason)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, repo.ErrNotPending) {
			msg = "该申请已处理，请刷新页面"
		}
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": msg})
			return
		}
		http.Redirect(w, r, "/admin/verifications/"+strconv.FormatInt(id, 10)+"?err="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	action := "real_name_rejected"
	detail := "reason=" + reason
	if approve {
		action = "real_name_approved"
		detail = ""
	}
	v, _ := h.Identity.Store.Submission(r.Context(), id)
	var target int64
	if v != nil {
		target = v.UserID
	}
	h.AdminLog.Record(sess.UserID, action, "real_name_submission", id, detail, requestIP(r))
	if target > 0 && !approve {
		h.AdminLog.Record(sess.UserID, "real_name_rejected_user", "user", target, "submission="+strconv.FormatInt(id, 10), requestIP(r))
	}
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "审核结果已保存"})
		return
	}
	http.Redirect(w, r, "/admin/verifications/"+strconv.FormatInt(id, 10)+"?ok="+url.QueryEscape("审核结果已保存"), http.StatusSeeOther)
}

func (h *AdminVerification) photo(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.require(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v, err := h.Identity.Store.Submission(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ref := v.FrontPhotoRef
	if r.PathValue("side") == "back" {
		ref = v.BackPhotoRef
	} else if r.PathValue("side") != "front" {
		http.NotFound(w, r)
		return
	}
	file, err := h.Identity.Files.Open(ref)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.AdminLog.Record(sess.UserID, "real_name_photo_viewed", "real_name_submission", id, r.PathValue("side"), requestIP(r))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasSuffix(strings.ToLower(ref), ".png") {
		w.Header().Set("Content-Type", "image/png")
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
	}
	w.Header().Set("Content-Disposition", `inline; filename="identity-photo"`)
	http.ServeContent(w, r, "identity-photo", info.ModTime(), file)
}
