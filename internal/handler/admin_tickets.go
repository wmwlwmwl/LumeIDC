package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"lumeidc/internal/middleware"
	"lumeidc/internal/repo"
)

func (a *Admin) adminTicketID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if _, ok := middleware.RequireAdmin(w, r); !ok || a.DB == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("ticketID"), 10, 64)
	if err != nil || id <= 0 {
		jsonStatus(w, r, http.StatusBadRequest, "工单 ID 无效")
		return 0, false
	}
	return id, true
}

// ticketSortCols 后台工单列表可排序字段白名单（prop → SQL 表达式）。
var ticketSortCols = map[string]string{
	"id": "t.id", "subject": "t.subject", "category": "t.category", "priority": "t.priority",
	"status": "t.status", "updated_at": "t.updated_at", "created_at": "t.created_at",
}

func (a *Admin) adminTickets(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok || a.DB == nil {
		return
	}
	where := ` WHERE 1=1`
	args := []any{}
	next := 1
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		where += ` AND (t.subject ILIKE $` + strconv.Itoa(next) + ` OR t.body ILIKE $` + strconv.Itoa(next) + ` OR u.email ILIKE $` + strconv.Itoa(next) + `)`
		args = append(args, "%"+q+"%")
		next++
	}
	if v := r.URL.Query().Get("status"); v != "" {
		where += ` AND t.status=$` + strconv.Itoa(next)
		args = append(args, v)
		next++
	}
	if v := r.URL.Query().Get("priority"); v != "" {
		where += ` AND t.priority=$` + strconv.Itoa(next)
		args = append(args, v)
		next++
	}
	if v := r.URL.Query().Get("category"); v != "" {
		where += ` AND t.category=$` + strconv.Itoa(next)
		args = append(args, v)
		next++
	}
	if v := r.URL.Query().Get("assignee_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			where += ` AND coalesce(t.assignee_admin_id,0)=$` + strconv.Itoa(next)
			args = append(args, id)
			next++
		}
	}
	if v := r.URL.Query().Get("service_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			where += ` AND coalesce(t.service_id,0)=$` + strconv.Itoa(next)
			args = append(args, id)
			next++
		}
	}
	if r.URL.Query().Get("queue") == "unassigned" {
		where += ` AND t.assignee_admin_id IS NULL`
	}
	if r.URL.Query().Get("queue") == "mine" {
		sess := middleware.FromSession(r.Context())
		where += ` AND t.assignee_admin_id=$` + strconv.Itoa(next)
		args = append(args, sess.UserID)
		next++
	}
	var total int
	if err := a.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM tickets t LEFT JOIN users u ON u.id=t.user_id`+where, args...).Scan(&total); err != nil {
		jsonStatus(w, r, 500, "查询工单失败")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 25
	}
	args = append(args, limit, (page-1)*limit)
	// 默认按待处理优先+最近更新；点表头排序时经白名单映射（repo.OrderByParam）
	orderBy := repo.OrderByParam(r.URL.Query().Get("sort"), r.URL.Query().Get("order"), ticketSortCols,
		` ORDER BY CASE t.status WHEN 'pending' THEN 0 WHEN 'processing' THEN 1 ELSE 2 END,t.updated_at DESC`)
	rows, err := a.DB.QueryContext(r.Context(), `SELECT t.id,t.user_id,coalesce(u.email,''),t.subject,t.priority,t.status,coalesce(t.category,''),coalesce(s.name,''),coalesce(s.hostname,''),coalesce(t.assignee_admin_id,0),coalesce(au.username,''),to_char(t.created_at,'YYYY-MM-DD HH24:MI'),to_char(t.updated_at,'YYYY-MM-DD HH24:MI') FROM tickets t LEFT JOIN users u ON u.id=t.user_id LEFT JOIN services s ON s.id=t.service_id LEFT JOIN admin_users au ON au.id=t.assignee_admin_id`+where+orderBy+` LIMIT $`+strconv.Itoa(next)+` OFFSET $`+strconv.Itoa(next+1), args...)
	if err != nil {
		jsonStatus(w, r, http.StatusInternalServerError, "查询工单失败")
		return
	}
	defer rows.Close()
	list := make([]map[string]any, 0)
	for rows.Next() {
		var id, userID int64
		var email, subject, priority, status, category, serviceName, hostname, assigneeName, created, updated string
		var assigneeID int64
		if err := rows.Scan(&id, &userID, &email, &subject, &priority, &status, &category, &serviceName, &hostname, &assigneeID, &assigneeName, &created, &updated); err != nil {
			jsonStatus(w, r, 500, "读取工单失败")
			return
		}
		list = append(list, map[string]any{"id": id, "user_id": userID, "email": email, "subject": subject, "priority": priority, "status": status, "category": category, "service_name": serviceName, "service_hostname": hostname, "assignee_id": assigneeID, "assignee_name": assigneeName, "created_at": created, "updated_at": updated})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": list, "total": total, "page": page, "limit": limit})
}

func (a *Admin) adminTicketStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok || a.DB == nil {
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT status,count(*) FROM tickets GROUP BY status`)
	if err != nil {
		jsonStatus(w, r, 500, "查询统计失败")
		return
	}
	defer rows.Close()
	stats := map[string]int{"pending": 0, "processing": 0, "closed": 0, "unassigned": 0, "overdue": 0}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err == nil {
			stats[status] = n
		}
	}
	var unassigned, overdue int
	// 统计查询失败时留日志：静默归零会让管理员把数据异常误判为"没有待处理工单"。
	if err := a.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM tickets WHERE assignee_admin_id IS NULL AND status<>'closed'`).Scan(&unassigned); err != nil {
		log.Printf("[tickets] 未分配统计查询失败: %v", err)
	}
	if err := a.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM tickets WHERE status<>'closed' AND updated_at < now()-interval '24 hours'`).Scan(&overdue); err != nil {
		log.Printf("[tickets] 超时统计查询失败: %v", err)
	}
	stats["unassigned"], stats["overdue"] = unassigned, overdue
	writeJSON(w, map[string]any{"ok": 1, "stats": stats})
}

func (a *Admin) adminTicketDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := a.adminTicketID(w, r)
	if !ok {
		return
	}
	var ticket map[string]any = map[string]any{}
	var userID int64
	var subject, body, priority, status, serviceName, hostname, created, updated string
	err := a.DB.QueryRowContext(r.Context(), `SELECT t.user_id,t.subject,t.body,t.priority,t.status,coalesce(s.name,''),coalesce(s.hostname,''),to_char(t.created_at,'YYYY-MM-DD HH24:MI'),to_char(t.updated_at,'YYYY-MM-DD HH24:MI') FROM tickets t LEFT JOIN services s ON s.id=t.service_id WHERE t.id=$1`, id).Scan(&userID, &subject, &body, &priority, &status, &serviceName, &hostname, &created, &updated)
	if err != nil {
		jsonStatus(w, r, http.StatusNotFound, "工单不存在")
		return
	}
	ticket = map[string]any{"id": id, "user_id": userID, "subject": subject, "body": body, "priority": priority, "status": status, "service_name": serviceName, "service_hostname": hostname, "created_at": created, "updated_at": updated}
	_, _ = a.DB.ExecContext(r.Context(), `UPDATE ticket_messages SET read_by_admin_at=now(),read_at=now() WHERE ticket_id=$1 AND author_type='user' AND read_by_admin_at IS NULL`, id)
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,coalesce(user_id,0),coalesce(admin_id,0),content,is_internal,to_char(created_at,'YYYY-MM-DD HH24:MI') FROM ticket_messages WHERE ticket_id=$1 ORDER BY id`, id)
	if err != nil {
		jsonStatus(w, r, 500, "读取回复失败")
		return
	}
	defer rows.Close()
	messages := make([]map[string]any, 0)
	for rows.Next() {
		var mid, uid, aid int64
		var content, createdAt string
		var internal bool
		if err := rows.Scan(&mid, &uid, &aid, &content, &internal, &createdAt); err != nil {
			jsonStatus(w, r, 500, "读取回复失败")
			return
		}
		messages = append(messages, map[string]any{"id": mid, "user_id": uid, "admin_id": aid, "content": content, "internal": internal, "created_at": createdAt})
	}
	attachments := make([]map[string]any, 0)
	attachmentRows, err := a.DB.QueryContext(r.Context(), `SELECT id,coalesce(message_id,0),original_name,mime,size FROM ticket_attachments WHERE ticket_id=$1 ORDER BY id`, id)
	if err == nil {
		defer attachmentRows.Close()
		for attachmentRows.Next() {
			var aid, mid, size int64
			var name, mimeType string
			if attachmentRows.Scan(&aid, &mid, &name, &mimeType, &size) == nil {
				attachments = append(attachments, map[string]any{"id": aid, "message_id": mid, "name": name, "mime": mimeType, "size": size, "url": "/tickets/" + strconv.FormatInt(id, 10) + "/attachments/" + strconv.FormatInt(aid, 10)})
			}
		}
	}
	history := make([]map[string]any, 0)
	historyRows, err := a.DB.QueryContext(r.Context(), `SELECT from_status,to_status,note,to_char(created_at,'YYYY-MM-DD HH24:MI') FROM ticket_status_history WHERE ticket_id=$1 ORDER BY id DESC LIMIT 30`, id)
	if err == nil {
		defer historyRows.Close()
		for historyRows.Next() {
			var from, to, note, at string
			if historyRows.Scan(&from, &to, &note, &at) == nil {
				history = append(history, map[string]any{"from": from, "to": to, "note": note, "time": at})
			}
		}
	}
	assignments := make([]map[string]any, 0)
	assignmentRows, err := a.DB.QueryContext(r.Context(), `SELECT coalesce(f.username,''),coalesce(t.username,''),to_char(h.created_at,'YYYY-MM-DD HH24:MI') FROM ticket_assignment_history h LEFT JOIN admin_users f ON f.id=h.from_admin_id LEFT JOIN admin_users t ON t.id=h.to_admin_id WHERE h.ticket_id=$1 ORDER BY h.id DESC LIMIT 30`, id)
	if err == nil {
		defer assignmentRows.Close()
		for assignmentRows.Next() {
			var from, to, at string
			if assignmentRows.Scan(&from, &to, &at) == nil {
				assignments = append(assignments, map[string]any{"from": from, "to": to, "time": at})
			}
		}
	}
	writeJSON(w, map[string]any{"ok": 1, "ticket": ticket, "messages": messages, "attachments": attachments, "history": history, "assignments": assignments})
}

func (a *Admin) adminTicketReply(w http.ResponseWriter, r *http.Request) {
	id, ok := a.adminTicketID(w, r)
	if !ok {
		return
	}
	sess := middleware.FromSession(r.Context())
	content, header, err := ticketReplyRequest(r)
	if err != nil {
		jsonStatus(w, r, 400, err.Error())
		return
	}
	if content == "" && header == nil {
		jsonStatus(w, r, 400, "回复内容不能为空")
		return
	}
	var userID int64
	if err := a.DB.QueryRowContext(r.Context(), `SELECT user_id FROM tickets WHERE id=$1`, id).Scan(&userID); err != nil {
		jsonStatus(w, r, 404, "工单不存在")
		return
	}
	ref, name, mimeType := "", "", ""
	var size int64
	if header != nil {
		if a.PrivateFiles == nil {
			jsonStatus(w, r, 500, "附件服务未配置")
			return
		}
		ref, name, mimeType, size, err = a.PrivateFiles.SaveAttachment(header)
		if err != nil {
			jsonStatus(w, r, 400, err.Error())
			return
		}
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	var messageID int64
	if err == nil {
		err = tx.QueryRowContext(r.Context(), `INSERT INTO ticket_messages(ticket_id,admin_id,content,author_type) VALUES($1,$2,$3,'admin') RETURNING id`, id, sess.UserID, content).Scan(&messageID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE tickets SET status='processing',updated_at=now(),last_reply_by='admin',first_response_at=coalesce(first_response_at,now()) WHERE id=$1`, id)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO ticket_status_history(ticket_id,from_status,to_status,admin_id,note) SELECT id,status,'processing',$2,'客服回复' FROM tickets WHERE id=$1 AND status<>'processing'`, id, sess.UserID)
	}
	if err == nil && ref != "" {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO ticket_attachments(ticket_id,message_id,admin_id,storage_ref,original_name,mime,size) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, messageID, sess.UserID, ref, name, mimeType, size)
	}
	if err == nil {
		err = tx.Commit()
	} else if tx != nil {
		_ = tx.Rollback()
	}
	if err != nil && ref != "" {
		_ = a.PrivateFiles.Delete(ref)
	}
	if err != nil {
		jsonStatus(w, r, 500, "回复失败")
		return
	}
	if a.Notifier != nil {
		var subject string
		_ = a.DB.QueryRowContext(r.Context(), `SELECT subject FROM tickets WHERE id=$1`, id).Scan(&subject)
		a.Notifier.TicketNotify(r.Context(), userID, "reply", subject)
	}
	writeJSON(w, map[string]any{"ok": 1})
}

func (a *Admin) adminTicketStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := a.adminTicketID(w, r)
	if !ok {
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		jsonStatus(w, r, 400, "请求格式无效")
		return
	}
	if input.Status != "pending" && input.Status != "processing" && input.Status != "closed" {
		jsonStatus(w, r, 400, "状态无效")
		return
	}
	var previous string
	if err := a.DB.QueryRowContext(r.Context(), `SELECT status FROM tickets WHERE id=$1`, id).Scan(&previous); err != nil {
		jsonStatus(w, r, 404, "工单不存在")
		return
	}
	if _, err := a.DB.ExecContext(r.Context(), `UPDATE tickets SET status=$1,closed_at=CASE WHEN $1='closed' THEN now() ELSE NULL END,updated_at=now(),last_reply_by='admin' WHERE id=$2`, input.Status, id); err != nil {
		jsonStatus(w, r, 500, "更新失败")
		return
	}
	_, _ = a.DB.ExecContext(r.Context(), `INSERT INTO ticket_status_history(ticket_id,from_status,to_status,admin_id) VALUES($1,$2,$3,$4)`, id, previous, input.Status, middleware.FromSession(r.Context()).UserID)
	writeJSON(w, map[string]any{"ok": 1})
}

func (a *Admin) adminTicketAssign(w http.ResponseWriter, r *http.Request) {
	id, ok := a.adminTicketID(w, r)
	if !ok {
		return
	}
	var input struct {
		AdminID int64 `json:"admin_id"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		jsonStatus(w, r, 400, "请求格式无效")
		return
	}
	if input.AdminID != 0 {
		var exists bool
		if err := a.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id=$1)`, input.AdminID).Scan(&exists); err != nil || !exists {
			jsonStatus(w, r, 400, "客服不存在")
			return
		}
	}
	var previous int64
	_ = a.DB.QueryRowContext(r.Context(), `SELECT coalesce(assignee_admin_id,0) FROM tickets WHERE id=$1`, id).Scan(&previous)
	if _, err := a.DB.ExecContext(r.Context(), `UPDATE tickets SET assignee_admin_id=NULLIF($1,0),updated_at=now() WHERE id=$2`, input.AdminID, id); err != nil {
		jsonStatus(w, r, 500, "分配失败")
		return
	}
	_, _ = a.DB.ExecContext(r.Context(), `INSERT INTO ticket_assignment_history(ticket_id,from_admin_id,to_admin_id,changed_by) VALUES($1,NULLIF($2,0),NULLIF($3,0),$4)`, id, previous, input.AdminID, middleware.FromSession(r.Context()).UserID)
	if a.Notifier != nil {
		var userID int64
		var subject string
		_ = a.DB.QueryRowContext(r.Context(), `SELECT user_id,subject FROM tickets WHERE id=$1`, id).Scan(&userID, &subject)
		a.Notifier.TicketNotify(r.Context(), userID, "assigned", subject)
	}
	writeJSON(w, map[string]any{"ok": 1})
}

func (a *Admin) adminTicketInternalNote(w http.ResponseWriter, r *http.Request) {
	id, ok := a.adminTicketID(w, r)
	if !ok {
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		jsonStatus(w, r, 400, "请求格式无效")
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" {
		jsonStatus(w, r, 400, "备注不能为空")
		return
	}
	sess := middleware.FromSession(r.Context())
	if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO ticket_messages(ticket_id,admin_id,content,is_internal) VALUES($1,$2,$3,true)`, id, sess.UserID, input.Content); err != nil {
		jsonStatus(w, r, 500, "保存备注失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1})
}

func (a *Admin) adminTicketAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := a.adminTicketID(w, r)
	if !ok || a.PrivateFiles == nil {
		return
	}
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		jsonStatus(w, r, 400, "附件请求无效")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		jsonStatus(w, r, 400, "请选择附件")
		return
	}
	defer file.Close()
	ref, name, mimeType, size, err := a.PrivateFiles.SaveAttachment(header)
	if err != nil {
		jsonStatus(w, r, 400, err.Error())
		return
	}
	sess := middleware.FromSession(r.Context())
	var aid int64
	if err := a.DB.QueryRowContext(r.Context(), `INSERT INTO ticket_attachments(ticket_id,admin_id,storage_ref,original_name,mime,size) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, id, sess.UserID, ref, name, mimeType, size).Scan(&aid); err != nil {
		_ = a.PrivateFiles.Delete(ref)
		jsonStatus(w, r, 500, "保存附件失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "id": aid, "name": name, "size": size})
}

func (a *Admin) adminTicketAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok || a.DB == nil || a.PrivateFiles == nil {
		return
	}
	aid, _ := strconv.ParseInt(r.PathValue("attachmentID"), 10, 64)
	var ref, name, mimeType string
	if err := a.DB.QueryRowContext(r.Context(), `SELECT storage_ref,original_name,mime FROM ticket_attachments WHERE id=$1`, aid).Scan(&ref, &name, &mimeType); err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := a.PrivateFiles.Open(ref)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(name, `"`, `'`)+`"`)
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func (a *Admin) adminTicketAssignees(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok || a.DB == nil {
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,username FROM admin_users ORDER BY id`)
	if err != nil {
		jsonStatus(w, r, 500, "查询客服失败")
		return
	}
	defer rows.Close()
	list := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			jsonStatus(w, r, 500, "查询客服失败")
			return
		}
		list = append(list, map[string]any{"id": id, "name": name})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": list})
}
