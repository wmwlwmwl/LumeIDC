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
	Identity *service.Identity
	Users    *repo.Users
	Sessions *middleware.Store
	AdminLog *repo.AdminLog
	*Deps
}

func (h *VerificationHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /user/profile", h.profile)
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
	if h.Identity == nil || h.Identity.Store == nil {
		http.Error(w, "实名服务未配置", http.StatusServiceUnavailable)
		return
	}
	phone, verified, err := h.Identity.Store.UserPhone(r.Context(), userID)
	if err != nil {
		http.Error(w, "读取账户信息失败", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Phone": phone, "PhoneMasked": service.MaskPhone(phone), "PhoneVerified": verified,
		"HasPhone": phone != "", "CSRF": csrfOf(h.Sessions, w, r),
		"Error": r.URL.Query().Get("err"), "OK": r.URL.Query().Get("ok"),
	}
	h.render(w, r, "user_profile.html", data)
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
	if h.Identity == nil || h.Identity.Store == nil || h.Users == nil {
		http.Redirect(w, r, "/user/profile?err="+url.QueryEscape("手机号服务未配置"), http.StatusSeeOther)
		return
	}
	phone, _, err := h.Identity.Store.UserPhone(r.Context(), userID)
	if err != nil {
		http.Redirect(w, r, "/user/profile?err="+url.QueryEscape("读取账户信息失败"), http.StatusSeeOther)
		return
	}
	purpose := "bind"
	if phone != "" {
		purpose = "change"
		hash, hashErr := h.Users.PasswordHash(r.Context(), userID)
		if hashErr != nil || !h.Users.VerifyPassword(hash, r.PostFormValue("current_password")) {
			http.Redirect(w, r, "/user/profile?err="+url.QueryEscape("当前密码错误"), http.StatusSeeOther)
			return
		}
	}
	if err := h.Identity.RequestPhoneCode(r.Context(), userID, r.PostFormValue("phone"), purpose, requestIP(r)); err != nil {
		http.Redirect(w, r, "/user/profile?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	h.AdminLog.Record(0, "phone_otp_requested", "user", userID, "purpose="+purpose, requestIP(r))
	http.Redirect(w, r, "/user/profile?ok="+url.QueryEscape("验证码已发送，请查收短信"), http.StatusSeeOther)
}

func (h *VerificationHandler) confirmPhone(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if h.Identity == nil || h.Identity.Store == nil || h.Users == nil {
		http.Redirect(w, r, "/user/profile?err="+url.QueryEscape("手机号服务未配置"), http.StatusSeeOther)
		return
	}
	phone, _, err := h.Identity.Store.UserPhone(r.Context(), userID)
	if err != nil {
		http.Redirect(w, r, "/user/profile?err="+url.QueryEscape("读取账户信息失败"), http.StatusSeeOther)
		return
	}
	purpose := "bind"
	if phone != "" {
		purpose = "change"
	}
	if err := h.Identity.ConfirmPhoneCode(r.Context(), userID, purpose, r.PostFormValue("code")); err != nil {
		http.Redirect(w, r, "/user/profile?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	h.AdminLog.Record(0, "phone_verified", "user", userID, "purpose="+purpose, requestIP(r))
	if purpose == "change" {
		h.AdminLog.Record(0, "phone_changed", "user", userID, "", requestIP(r))
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
	data := map[string]any{
		"ManualEnabled": true, "Status": status, "StatusText": service.StatusText(status), "Reason": reason,
		"MaskedID": maskedID, "CanSubmit": status == "" || status == "rejected",
		"PluginProvider": "", "AutomaticStatus": "", "AutomaticStatusText": "", "AutomaticID": int64(0), "AutomaticURL": "",
		"CSRF": csrfOf(h.Sessions, w, r), "Error": r.URL.Query().Get("err"), "OK": r.URL.Query().Get("ok"),
	}
	if auto != nil {
		data["AutomaticProvider"] = auto.ProviderKey
		data["AutomaticStatus"] = auto.Status
		data["AutomaticStatusText"] = service.StatusText(auto.Status)
		data["AutomaticID"] = auto.ID
		data["AutomaticURL"] = auto.ProviderURL
	}
	if h.Identity.Settings != nil {
		if enabled, err := h.Identity.Settings.Get(r.Context(), "manual_identity_enabled"); err == nil && enabled == "0" {
			data["ManualEnabled"] = false
		}
		provider, _ := h.Identity.Settings.Get(r.Context(), "verification_provider")
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider != "" && provider != "manual" {
			data["PluginProvider"] = provider
		}
	}
	h.render(w, r, "user_verification.html", data)
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
		http.Redirect(w, r, "/user/verification?err="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	h.AdminLog.Record(0, "real_name_submitted", "user", userID, "source=manual", requestIP(r))
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
	provider := strings.TrimSpace(r.PostFormValue("provider"))
	callback := siteBaseURL(r.Context(), h.Settings, r) + "/user/verification" // 站点地址优先，否则按请求推断
	id, urlValue, err := h.Identity.StartProvider(r.Context(), userID, provider, service.RealNameForm{LegalName: r.PostFormValue("legal_name"), IdentityNumber: r.PostFormValue("identity_number")}, callback)
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
	id, err := strconv.ParseInt(r.PostFormValue("submission_id"), 10, 64)
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
	sess, ok := middleware.RequireAdmin(w, r)
	if !ok {
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
		http.Error(w, "查询实名申请失败", http.StatusInternalServerError)
		return
	}
	for i := range rows {
		rows[i].Phone = service.MaskPhone(rows[i].Phone)
	}
	h.renderAdmin(w, "admin_verifications.html", AdminData{Rows: rows, CSRF: h.adminCSRF(w, r), Error: r.URL.Query().Get("err"), Msg: r.URL.Query().Get("ok")})
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
	data := AdminData{CSRF: h.adminCSRF(w, r), Error: r.URL.Query().Get("err"), Msg: r.URL.Query().Get("ok"), ServersList: map[string]any{
		"ID": v.ID, "UserID": v.UserID, "Email": v.Email, "Phone": service.MaskPhone(v.Phone),
		"Status": service.StatusText(v.Status), "RawStatus": v.Status, "Name": name,
		"IdentityNumber": service.MaskIdentityNumber(identityNumber), "SubmittedAt": v.SubmittedAt.Format("2006-01-02 15:04"),
		"Reason":   v.RejectionReason,
		"FrontURL": "/admin/verifications/" + strconv.FormatInt(v.ID, 10) + "/photo/front",
		"BackURL":  "/admin/verifications/" + strconv.FormatInt(v.ID, 10) + "/photo/back",
	}}
	h.renderAdmin(w, "admin_verification_detail.html", data)
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
	if tok := r.PostFormValue("_csrf"); tok == "" || !checkCSRF(r, tok) {
		middleware.RedirectToLogin(w, r, "页面已过期，请重新登录后重试")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = h.Identity.Review(r.Context(), id, sess.UserID, approve, strings.TrimSpace(r.PostFormValue("reason")))
	if err != nil {
		msg := err.Error()
		if errors.Is(err, repo.ErrNotPending) {
			msg = "该申请已处理，请刷新页面"
		}
		http.Redirect(w, r, "/admin/verifications/"+strconv.FormatInt(id, 10)+"?err="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	action := "real_name_rejected"
	detail := "reason=" + strings.TrimSpace(r.PostFormValue("reason"))
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
