package handler

import (
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
	list, total, err := m.Users.ListUsersPage(r.Context(), q, per, (page-1)*per)
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	var rows []adminRow
	for _, u := range list {
		statusText := "正常"
		if u.Status != 1 {
			statusText = "禁用"
		}
		phone := "-"
		if u.Phone != "" {
			phone = service.MaskPhone(u.Phone)
		}
		rows = append(rows, adminRow{
			ID: u.ID, A: u.Email, B: u.Name, C: phone,
			D: statusText, E: fmt.Sprintf("%.2f", u.Balance), F: u.CreatedAt,
		})
	}
	pager := pagerFor(r, "/admin/users", per, total)
	pager.Q = q
	m.renderAdmin(w, "admin_users.html", AdminData{Rows: rows, Pager: &pager})
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
	// 实名认证（对齐 ZJMF：管理员可看完整姓名/证件号 + 证件照片）
	realNameStatus := "未提交"
	verifName, verifNumber := "", ""
	verifSubID := int64(0)
	verifSubmittedAt, verifReviewedAt := "", ""
	if m.Identity != nil && m.IdentitySvc != nil {
		if v, verr := m.Identity.CurrentVerification(r.Context(), id); verr == nil && v != nil {
			realNameStatus = service.StatusText(v.Status)
			verifSubID = v.ID
			verifSubmittedAt = v.SubmittedAt.Format("2006-01-02 15:04")
			if v.ReviewedAt.Valid {
				verifReviewedAt = v.ReviewedAt.Time.Format("2006-01-02 15:04")
			}
			if sub, nm, num, derr := m.IdentitySvc.AdminSubmission(r.Context(), v.ID); derr == nil && sub != nil {
				verifName, verifNumber = nm, num
			} else if derr != nil {
				log.Printf("[admin] 实名资料解密失败 user=%d: %v", id, derr)
			}
		} else if verr != nil {
			log.Printf("[admin] 实名状态查询失败 id=%d: %v", id, verr)
		}
	}
	if verifNumber != "" {
		m.audit(r, "real_name_viewed", "user", id, "admin_user_detail")
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
	serversList := map[string]any{
		"ID": id, "Email": user.Email, "A": user.Email, "B": user.Name,
		"C": fmt.Sprintf("%.2f", user.Balance), "D": itoa(int64(user.Status)),
		"Phone": user.Phone, "PhoneMasked": service.MaskPhone(user.Phone),
		"PhoneStatus": phoneStatus, "EmailStatus": emailStatus,
		"RegisteredAt": registeredAt, "LastLoginAt": lastLogin, "RealNameStatus": realNameStatus,
		"ServiceCount": serviceCount, "ActiveCount": activeCount,
		"UnpaidCount": unpaidCount, "PaidTotal": paidTotal,
		"BalanceLogs": logs, "AdminLogs": adminLogs,
		"VerifName": verifName, "VerifNumber": verifNumber,
		"VerifSubmittedAt": verifSubmittedAt, "VerifReviewedAt": verifReviewedAt,
	}
	if verifSubID > 0 {
		serversList["VerifFrontURL"] = "/admin/verifications/" + strconv.FormatInt(verifSubID, 10) + "/photo/front"
		serversList["VerifBackURL"] = "/admin/verifications/" + strconv.FormatInt(verifSubID, 10) + "/photo/back"
	}
	m.renderAdmin(w, "admin_user_form.html", AdminData{
		CSRF:        m.adminCSRF(w, r),
		Error:       r.URL.Query().Get("err"),
		ServersList: serversList,
	})
}

// UserSave POST /admin/users/{id}/save — 联系方式、状态/密码/余额调整。

func (m *AdminManage) UserSave(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		userEditError(w, r, id, "表单解析失败")
		return
	}
	current, err := m.Users.AdminUserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("[admin] 用户查询失败 id=%d: %v", id, err)
		userEditError(w, r, id, "读取用户失败，请稍后重试")
		return
	}

	email := strings.TrimSpace(r.PostFormValue("email"))
	if email != "" {
		email, err = repo.NormalizeEmail(email)
		if err != nil {
			userEditError(w, r, id, "邮箱格式不正确")
			return
		}
	}
	if email != "" && email != current.Email {
		taken, checkErr := m.Users.EmailTaken(r.Context(), email, id)
		if checkErr != nil {
			userEditError(w, r, id, "检查邮箱失败，请稍后重试")
			return
		}
		if taken {
			userEditError(w, r, id, "邮箱已被占用")
			return
		}
	}

	phoneInput := strings.TrimSpace(r.PostFormValue("phone"))
	phone := ""
	if phoneInput != "" {
		phone, err = service.NormalizePhone(phoneInput)
		if err != nil {
			userEditError(w, r, id, "手机号格式不正确")
			return
		}
		if m.Identity == nil {
			userEditError(w, r, id, "手机号服务未配置")
			return
		}
		taken, checkErr := m.Identity.PhoneTaken(r.Context(), phone, id)
		if checkErr != nil {
			userEditError(w, r, id, "检查手机号失败，请稍后重试")
			return
		}
		if taken {
			userEditError(w, r, id, "手机号已被占用")
			return
		}
	}

	if email == "" && phoneInput == "" {
		userEditError(w, r, id, "邮箱或手机号至少填写一个")
		return
	}

	status := r.PostFormValue("status")
	if status != "0" && status != "1" {
		userEditError(w, r, id, "账号状态无效")
		return
	}
	password := r.PostFormValue("new_password")
	if password != "" && len(password) < 8 {
		userEditError(w, r, id, "密码至少8位")
		return
	}
	balanceAdjust := strings.TrimSpace(r.PostFormValue("balance_adjust"))
	if !validAdminBalanceAdjustment(balanceAdjust) {
		userEditError(w, r, id, "余额调整金额无效")
		return
	}

	emailChanged := email != current.Email
	phoneChanged := phone != current.Phone
	if phoneChanged && m.Identity == nil {
		userEditError(w, r, id, "手机号服务未配置")
		return
	}
	if emailChanged {
		if err := m.Users.UpdateEmail(r.Context(), id, email); err != nil {
			userEditError(w, r, id, "保存邮箱失败，请稍后重试")
			return
		}
	}
	if phoneChanged {
		changed, err := m.Identity.AdminSetPhone(r.Context(), id, phone, time.Now())
		if err != nil {
			if errors.Is(err, repo.ErrPhoneInUse) {
				userEditError(w, r, id, "手机号已被占用")
			} else {
				userEditError(w, r, id, "保存手机号失败，请稍后重试")
			}
			return
		}
		phoneChanged = changed
	}
	statusChanged := (status == "1") != (current.Status == 1)
	if statusChanged {
		if err := m.Users.SetStatus(r.Context(), id, status == "1"); err != nil {
			userEditError(w, r, id, "保存状态失败")
			return
		}
	}
	if password != "" {
		if err := m.Users.ResetPassword(r.Context(), id, password); err != nil {
			userEditError(w, r, id, "重置密码失败")
			return
		}
	}
	if balanceAdjust != "" {
		if err := m.Balance.AdminAdjust(r.Context(), id, balanceAdjust, "管理员调整"); err != nil {
			userEditError(w, r, id, "余额调整失败")
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
	m.audit(r, "user_update", "user", id, strings.Join(changes, ","))
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// OrderRefund POST /admin/orders/{id}/refund — 管理员退款。
