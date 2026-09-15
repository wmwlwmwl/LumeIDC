package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	moneyutil "lumeidc/internal/money"
	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

func (m *AdminManage) UsersList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	const per = 25
	page := pageParam(r)
	q := r.URL.Query().Get("q")
	list, total, err := m.Users.ListUsersPage(r.Context(), q, r.URL.Query().Get("sort"), r.URL.Query().Get("order"), per, (page-1)*per)
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	type userJSON struct {
		ID       int64  `json:"id"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Phone    string `json:"phone"`
		Status   string `json:"status"`
		Balance  string `json:"balance"`
		Realname string `json:"realname"`
		Created  string `json:"created_at"`
		Disabled bool   `json:"disabled"`
	}
	// 批量查实名状态（人工/自动任一通过=approved，否则待审=pending，否则 none）。
	ids := make([]int64, 0, len(list))
	for _, u := range list {
		ids = append(ids, u.ID)
	}
	realname := map[int64]string{}
	if m.Identity != nil {
		if st, err := m.Identity.RealnameStatuses(r.Context(), ids); err == nil {
			realname = st
		} else {
			log.Printf("[admin] 实名状态批量查询失败: %v", err)
		}
	}
	items := make([]userJSON, 0, len(list))
	for _, u := range list {
		statusText := "正常"
		disabled := false
		if u.Status != 1 {
			statusText = "禁用"
			disabled = true
		}
		phone := "-"
		if u.Phone != "" {
			phone = service.MaskPhone(u.Phone)
		}
		rn := realname[u.ID]
		if rn == "" {
			rn = "none"
		}
		items = append(items, userJSON{
			ID: u.ID, Email: u.Email, Name: u.Name, Phone: phone,
			Status: statusText, Balance: fmt.Sprintf("%.2f", u.Balance), Realname: rn,
			Created: u.CreatedAt, Disabled: disabled,
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": items, "total": total, "page": page})
}

func validAdminBalanceAdjustment(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	if strings.HasPrefix(raw, "+") || strings.HasPrefix(raw, "-") {
		raw = raw[1:]
	}
	if raw == "" {
		return false
	}
	_, _, err := moneyutil.ParsePositive(raw, 999999999999)
	return err == nil
}

func userEditError(w http.ResponseWriter, r *http.Request, id int64, msg string) {
	http.Redirect(w, r, "/admin/users/"+itoa(id)+"/edit?err="+url.QueryEscape(msg), http.StatusSeeOther)
}

func normalizeAmount(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0.00"
	}
	return s
}

func (m *AdminManage) UserEdit(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user, err := m.Users.AdminUserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("[admin] 用户查询失败 id=%d: %v", id, err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	phoneStatus := "未绑定"
	if user.Phone != "" {
		phoneStatus = "未验证"
		if user.PhoneVerified {
			phoneStatus = "已验证"
		}
	}
	emailStatus := "未验证"
	if user.EmailVerified {
		emailStatus = "已验证"
	}
	// 只读信息区块（best-effort：任一查询失败仅记日志，不阻断详情页渲染）
	registeredAt, lastLogin := "-", "-"
	if !user.CreatedAt.IsZero() {
		registeredAt = user.CreatedAt.Format("2006-01-02 15:04")
	}
	if user.LastLoginAt.Valid {
		lastLogin = user.LastLoginAt.Time.Format("2006-01-02 15:04")
	}
	// 实名认证：详情页只返回状态摘要，不在此解密姓名/证件号，也不写审计。
	// 管理员点击「查看实名资料」时再调用 UserRealname，解密并记录 real_name_viewed。
	realNameStatus := "未提交"
	verifSubID := int64(0)
	verifSubmittedAt, verifReviewedAt := "", ""
	if m.Identity != nil {
		if v, verr := m.Identity.CurrentVerification(r.Context(), id); verr == nil && v != nil {
			realNameStatus = service.StatusText(v.Status)
			verifSubID = v.ID
			verifSubmittedAt = v.SubmittedAt.Format("2006-01-02 15:04")
			if v.ReviewedAt.Valid {
				verifReviewedAt = v.ReviewedAt.Time.Format("2006-01-02 15:04")
			}
		} else if verr != nil {
			log.Printf("[admin] 实名状态查询失败 id=%d: %v", id, verr)
		}
	}
	serviceCount, activeCount, unpaidCount, paidTotal := m.Svc.UserStats(r.Context(), id)
	logs, _ := m.Balance.Logs(r.Context(), id)
	if logs == nil {
		logs = []map[string]any{}
	}
	var adminLogs []repo.AdminLogRow
	if m.AdminLog != nil {
		if al, aerr := m.AdminLog.ListByTarget(r.Context(), "user", id, 20); aerr == nil {
			adminLogs = al
		} else {
			log.Printf("[admin] 审计日志查询失败 id=%d: %v", id, aerr)
		}
	}
	adminLogsJSON := make([]map[string]any, 0, len(adminLogs))
	for _, l := range adminLogs {
		adminLogsJSON = append(adminLogsJSON, map[string]any{
			"action": l.Action, "detail": l.Detail, "admin_name": l.AdminName,
			"created_at": l.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	writeJSON(w, map[string]any{
		"ok": 1,
		"user": map[string]any{
			"id": id, "email": user.Email, "name": user.Name,
			"balance": fmt.Sprintf("%.2f", user.Balance), "status": user.Status,
			"phone": user.Phone, "phone_masked": service.MaskPhone(user.Phone),
			"phone_status": phoneStatus, "email_status": emailStatus,
			"email_verified": user.EmailVerified, "phone_verified": user.PhoneVerified,
			"registered_at": registeredAt, "last_login_at": lastLogin,
		},
		"realname": map[string]any{
			"status":       realNameStatus,
			"submitted_at": verifSubmittedAt, "reviewed_at": verifReviewedAt,
			"submission_id": verifSubID, "has_submission": verifSubID > 0,
		},
		"stats": map[string]any{
			"service_count": serviceCount, "active_count": activeCount,
			"unpaid_count": unpaidCount, "paid_total": paidTotal,
		},
		"balance_logs": logs,
		"admin_logs":   adminLogsJSON,
	})
}

// UserSave POST /admin/users/{id}/save — 联系方式、状态/密码/余额调整。

// userSaveInput 校验通过后的用户编辑表单字段。
type userSaveInput struct {
	email, phone, status, password, balanceAdjust string
	emailVerifiedRaw, phoneVerifiedRaw            string
}

// validateUserSaveInput 校验用户编辑表单：邮箱/手机号格式与占用查重、状态、密码长度、余额调整。
// 返回 err 时其消息可直接展示给管理员（fail 前置校验集中在此，保存步骤留在 UserSave）。
func (m *AdminManage) validateUserSaveInput(ctx context.Context, id int64, current *repo.AdminUser, fv func(string) string) (userSaveInput, error) {
	email := strings.TrimSpace(fv("email"))
	var err error
	if email != "" {
		email, err = repo.NormalizeEmail(email)
		if err != nil {
			return userSaveInput{}, errors.New("邮箱格式不正确")
		}
	}
	if email != "" && email != current.Email {
		taken, checkErr := m.Users.EmailTaken(ctx, email, id)
		if checkErr != nil {
			return userSaveInput{}, errors.New("检查邮箱失败，请稍后重试")
		}
		if taken {
			return userSaveInput{}, errors.New("邮箱已被占用")
		}
	}

	phoneInput := strings.TrimSpace(fv("phone"))
	phone := ""
	if phoneInput != "" {
		phone, err = service.NormalizePhone(phoneInput)
		if err != nil {
			return userSaveInput{}, errors.New("手机号格式不正确")
		}
		if m.Identity == nil {
			return userSaveInput{}, errors.New("手机号服务未配置")
		}
		taken, checkErr := m.Identity.PhoneTaken(ctx, phone, id)
		if checkErr != nil {
			return userSaveInput{}, errors.New("检查手机号失败，请稍后重试")
		}
		if taken {
			return userSaveInput{}, errors.New("手机号已被占用")
		}
	}

	if email == "" && phoneInput == "" {
		return userSaveInput{}, errors.New("邮箱或手机号至少填写一个")
	}

	status := fv("status")
	if status != "0" && status != "1" {
		return userSaveInput{}, errors.New("账号状态无效")
	}
	password := fv("new_password")
	if password != "" && len(password) < 8 {
		return userSaveInput{}, errors.New("密码至少8位")
	}
	balanceAdjust := strings.TrimSpace(fv("balance_adjust"))
	if !validAdminBalanceAdjustment(balanceAdjust) {
		return userSaveInput{}, errors.New("余额调整金额无效")
	}
	// 邮箱/手机号验证状态（""=不修改）：仅接受 "0"/"1"。
	emailVerifiedRaw := fv("email_verified")
	if emailVerifiedRaw != "" && emailVerifiedRaw != "0" && emailVerifiedRaw != "1" {
		return userSaveInput{}, errors.New("邮箱验证状态无效")
	}
	phoneVerifiedRaw := fv("phone_verified")
	if phoneVerifiedRaw != "" && phoneVerifiedRaw != "0" && phoneVerifiedRaw != "1" {
		return userSaveInput{}, errors.New("手机号验证状态无效")
	}
	// 未绑定手机号不能标记为已验证。
	if phoneVerifiedRaw == "1" && (phone == "" || m.Identity == nil) {
		return userSaveInput{}, errors.New("该用户未绑定手机号，无法标记为已验证")
	}
	// 未绑定邮箱同理。
	if emailVerifiedRaw == "1" && email == "" {
		return userSaveInput{}, errors.New("该用户未绑定邮箱，无法标记为已验证")
	}
	return userSaveInput{email: email, phone: phone, status: status, password: password, balanceAdjust: balanceAdjust, emailVerifiedRaw: emailVerifiedRaw, phoneVerifiedRaw: phoneVerifiedRaw}, nil
}

func (m *AdminManage) UserSave(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	vals := jsonVals(r)
	if vals == nil {
		if !m.requireCSRF(w, r) {
			return
		}
		if err := r.ParseForm(); err != nil {
			userEditError(w, r, id, "表单解析失败")
			return
		}
	}
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	fail := func(msg string) {
		if wantsJSON(r) {
			writeJSON(w, map[string]any{"ok": 0, "msg": msg})
			return
		}
		userEditError(w, r, id, msg)
	}
	current, err := m.Users.AdminUserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("[admin] 用户查询失败 id=%d: %v", id, err)
		fail("读取用户失败，请稍后重试")
		return
	}

	in, err := m.validateUserSaveInput(r.Context(), id, current, fv)
	if err != nil {
		fail(err.Error())
		return
	}
	email, phone, status, password, balanceAdjust := in.email, in.phone, in.status, in.password, in.balanceAdjust

	emailChanged := email != current.Email
	phoneChanged := phone != current.Phone
	if phoneChanged && m.Identity == nil {
		fail("手机号服务未配置")
		return
	}
	if emailChanged {
		if err := m.Users.UpdateEmail(r.Context(), id, email); err != nil {
			fail("保存邮箱失败，请稍后重试")
			return
		}
	}
	if phoneChanged {
		changed, err := m.Identity.AdminSetPhone(r.Context(), id, phone, time.Now())
		if err != nil {
			if errors.Is(err, repo.ErrPhoneInUse) {
				fail("手机号已被占用")
			} else {
				fail("保存手机号失败，请稍后重试")
			}
			return
		}
		phoneChanged = changed
	}
	statusChanged := (status == "1") != (current.Status == 1)
	if statusChanged {
		if err := m.Users.SetStatus(r.Context(), id, status == "1"); err != nil {
			fail("保存状态失败")
			return
		}
	}
	if password != "" {
		if err := m.Users.ResetPassword(r.Context(), id, password); err != nil {
			fail("重置密码失败")
			return
		}
	}
	if balanceAdjust != "" {
		if err := m.Balance.AdminAdjust(r.Context(), id, balanceAdjust, "管理员调整"); err != nil {
			fail("余额调整失败")
			return
		}
	}
	// 邮箱/手机号验证状态：在号码保存之后应用（改邮箱会复位 email_verified、换绑会复位手机验证），
	// 以管理员本次显式指定为准。
	if in.emailVerifiedRaw == "1" {
		if err := m.Users.SetEmailVerified(r.Context(), id, true); err != nil {
			fail("保存邮箱验证状态失败")
			return
		}
	} else if in.emailVerifiedRaw == "0" {
		if err := m.Users.SetEmailVerified(r.Context(), id, false); err != nil {
			fail("保存邮箱验证状态失败")
			return
		}
	}
	if in.phoneVerifiedRaw == "1" {
		if err := m.Users.SetPhoneVerified(r.Context(), id, true); err != nil {
			fail("保存手机号验证状态失败")
			return
		}
	} else if in.phoneVerifiedRaw == "0" {
		if err := m.Users.SetPhoneVerified(r.Context(), id, false); err != nil {
			fail("保存手机号验证状态失败")
			return
		}
	}

	if m.AdminStore != nil && (emailChanged || phoneChanged || password != "" || status == "0") {
		m.AdminStore.RevokeUser(id)
	}
	changes := make([]string, 0, 5)
	if emailChanged {
		changes = append(changes, "email_changed=true")
	}
	if phoneChanged {
		if phone == "" {
			changes = append(changes, "phone_cleared=true")
		} else {
			changes = append(changes, "phone_changed=true,phone_verified=false")
		}
	}
	if statusChanged {
		changes = append(changes, "status_changed=true")
	}
	if password != "" {
		changes = append(changes, "password_reset=true")
	}
	if balanceAdjust != "" {
		changes = append(changes, "balance_adjusted=true")
	}
	if in.emailVerifiedRaw == "1" {
		changes = append(changes, "email_verified=set")
	} else if in.emailVerifiedRaw == "0" {
		changes = append(changes, "email_verified=cleared")
	}
	if in.phoneVerifiedRaw == "1" {
		changes = append(changes, "phone_verified=set")
	} else if in.phoneVerifiedRaw == "0" {
		changes = append(changes, "phone_verified=cleared")
	}
	m.audit(r, "user_update", "user", id, strings.Join(changes, ","))
	if wantsJSON(r) {
		writeJSON(w, map[string]any{"ok": 1, "msg": "已保存"})
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// UserRealname GET /admin/users/{id}/realname — 查看用户当前实名资料。
// 仅在管理员主动点击「查看实名资料」时调用，解密姓名/证件号并记录 real_name_viewed 审计。
func (m *AdminManage) UserRealname(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		jsonStatus(w, r, 400, "用户参数无效")
		return
	}
	if m.Identity == nil || m.IdentitySvc == nil {
		writeJSON(w, map[string]any{"ok": 1, "realname": nil})
		return
	}
	v, verr := m.Identity.CurrentVerification(r.Context(), id)
	if verr != nil {
		jsonStatus(w, r, 500, "查询实名失败")
		return
	}
	if v == nil {
		writeJSON(w, map[string]any{"ok": 1, "realname": nil})
		return
	}
	sub, name, number, derr := m.IdentitySvc.AdminSubmission(r.Context(), v.ID)
	if derr != nil || sub == nil {
		jsonStatus(w, r, 500, "读取实名资料失败")
		return
	}
	m.audit(r, "real_name_viewed", "user", id, "admin_user_detail")
	reviewedAt := ""
	if v.ReviewedAt.Valid {
		reviewedAt = v.ReviewedAt.Time.Format("2006-01-02 15:04")
	}
	sid := strconv.FormatInt(v.ID, 10)
	writeJSON(w, map[string]any{
		"ok": 1,
		"realname": map[string]any{
			"status":        service.StatusText(v.Status),
			"name":          name,
			"number":        number,
			"submitted_at":  v.SubmittedAt.Format("2006-01-02 15:04"),
			"reviewed_at":   reviewedAt,
			"submission_id": v.ID,
			"front_url":     "/admin/verifications/" + sid + "/photo/front",
			"back_url":      "/admin/verifications/" + sid + "/photo/back",
		},
	})
}

// UserSetStatus POST /admin/users/{id}/status — 管理员启用/禁用用户。
func (m *AdminManage) UserSetStatus(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		jsonStatus(w, r, 400, "用户参数无效")
		return
	}
	vals := jsonVals(r)
	if vals == nil {
		if !m.requireCSRF(w, r) {
			return
		}
	}
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	// disabled=1 表示禁用，disabled=0 表示启用。
	enable := fv("disabled") != "1"
	if _, err := m.Users.AdminUserByID(r.Context(), id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			jsonStatus(w, r, 404, "用户不存在")
		} else {
			jsonStatus(w, r, 500, "读取用户失败")
		}
		return
	}
	if err := m.Users.SetStatus(r.Context(), id, enable); err != nil {
		writeJSON(w, map[string]any{"ok": 0, "msg": "保存状态失败"})
		return
	}
	if !enable && m.AdminStore != nil {
		m.AdminStore.RevokeUser(id)
	}
	if enable {
		m.audit(r, "user_enable", "user", id, "status=enabled")
	} else {
		m.audit(r, "user_disable", "user", id, "status=disabled")
	}
	msg := "已启用"
	if !enable {
		msg = "已禁用"
	}
	writeJSON(w, map[string]any{"ok": 1, "msg": msg})
}

// UserRecharge POST /admin/users/{id}/recharge — 管理员给用户充值余额。
func (m *AdminManage) UserRecharge(w http.ResponseWriter, r *http.Request) {
	m.userBalanceOp(w, r, "recharge")
}

// UserRefund POST /admin/users/{id}/refund — 管理员从用户余额退款（扣减）。
func (m *AdminManage) UserRefund(w http.ResponseWriter, r *http.Request) {
	m.userBalanceOp(w, r, "refund")
}

func (m *AdminManage) userBalanceOp(w http.ResponseWriter, r *http.Request, op string) {
	if !m.require(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		jsonStatus(w, r, 400, "用户参数无效")
		return
	}
	vals := jsonVals(r)
	if vals == nil {
		if !m.requireCSRF(w, r) {
			return
		}
	}
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	amount, _, err := moneyutil.ParsePositive(strings.TrimSpace(fv("amount")), 999999999999)
	if err != nil {
		jsonStatus(w, r, 400, "金额无效（最多两位小数且大于 0）")
		return
	}
	note := strings.TrimSpace(fv("note"))
	if _, err := m.Users.AdminUserByID(r.Context(), id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			jsonStatus(w, r, 404, "用户不存在")
		} else {
			jsonStatus(w, r, 500, "读取用户失败")
		}
		return
	}
	switch op {
	case "recharge":
		if note == "" {
			note = "管理员充值"
		}
		err = m.Balance.Recharge(r.Context(), id, amount, note)
	case "refund":
		if note == "" {
			note = "管理员退款"
		}
		// 余额扣减：负数调整，余额不足时 AdminAdjust 返回 ErrInsufficientBalance。
		err = m.Balance.AdminAdjust(r.Context(), id, "-"+amount, note)
	default:
		jsonStatus(w, r, 400, "操作无效")
		return
	}
	if err != nil {
		msg := err.Error()
		if errors.Is(err, repo.ErrInsufficientBalance) {
			msg = "余额不足，无法退款"
		}
		writeJSON(w, map[string]any{"ok": 0, "msg": msg})
		return
	}
	m.audit(r, "balance_"+op, "user", id, amount+", "+note)
	writeJSON(w, map[string]any{"ok": 1, "msg": "操作成功"})
}

// OrderRefund POST /admin/orders/{id}/refund — 管理员退款。
