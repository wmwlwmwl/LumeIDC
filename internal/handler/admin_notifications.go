package handler

import (
	"database/sql"
	"log"
	"net/http"

	"lumeidc/internal/middleware"
)

// AdminNotifications GET /admin/notifications — 后台通知中心（Art 顶栏消息面板）。
// 返回待办（停用申请/实名待审核）、消息（工单）、通知（公告）与待办总数。
// ponytail: 面板为 60s 轮询的只读聚合，单组查询失败只记日志并返回其余分组
// （可用性优先）；持续为空需查服务端日志，不向前端暴露部分失败状态。
func (a *Admin) adminNotifications(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.RequireAdmin(w, r); !ok || a.DB == nil {
		return
	}
	ctx := r.Context()
	todos := make([]map[string]any, 0)
	notices := make([]map[string]any, 0)
	messages := make([]map[string]any, 0)
	pending := 0

	// count 执行计数查询，失败记日志并按 0 处理
	count := func(query string) int {
		var n int
		if err := a.DB.QueryRowContext(ctx, query).Scan(&n); err != nil {
			log.Printf("[notifications] 计数查询失败: %v", err)
		}
		return n
	}
	// collect 执行列表查询并逐行构造 map；查询/扫描/遍历错误均记日志，不中断其余分组。
	// rows 在返回时即关闭，不占用连接池连接至请求结束。
	collect := func(dst *[]map[string]any, query string, scan func(*sql.Rows) (map[string]any, error)) {
		rows, err := a.DB.QueryContext(ctx, query)
		if err != nil {
			log.Printf("[notifications] 查询失败: %v", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scan(rows)
			if err != nil {
				log.Printf("[notifications] 读取行失败: %v", err)
				continue
			}
			*dst = append(*dst, m)
		}
		if err := rows.Err(); err != nil {
			log.Printf("[notifications] 遍历失败: %v", err)
		}
	}

	// 停用申请（待处理）→ 待办
	pending += count(`SELECT count(*) FROM service_cancel_requests WHERE status='pending'`)
	collect(&todos,
		`SELECT c.id, s.name, coalesce(nullif(u.name,''), u.email), to_char(c.created_at,'YYYY-MM-DD HH24:MI')
		 FROM service_cancel_requests c
		 JOIN services s ON s.id=c.service_id
		 JOIN users u ON u.id=c.user_id
		 WHERE c.status='pending' ORDER BY c.id DESC LIMIT 10`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var svc, user, t string
			if err := rows.Scan(&id, &svc, &user, &t); err != nil {
				return nil, err
			}
			return map[string]any{
				"type": "cancel", "title": "停用申请 · " + svc + "（" + user + "）",
				"time": t, "link": "/cancel-requests",
			}, nil
		},
	)

	// 实名待审核 → 待办
	pending += count(`SELECT count(*) FROM manual_identity_submissions WHERE status='pending'`)
	collect(&todos,
		`SELECT v.id, coalesce(u.email,''), to_char(v.submitted_at,'YYYY-MM-DD HH24:MI')
		 FROM manual_identity_submissions v
		 JOIN users u ON u.id=v.user_id
		 WHERE v.status='pending' ORDER BY v.submitted_at ASC LIMIT 10`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var email, t string
			if err := rows.Scan(&id, &email, &t); err != nil {
				return nil, err
			}
			return map[string]any{
				"type": "verification", "title": "实名待审核 · " + email,
				"time": t, "link": "/verifications",
			}, nil
		},
	)

	// 工单（未关闭）→ 消息
	pending += count(`SELECT count(*) FROM tickets WHERE status='pending'`)
	collect(&messages,
		`SELECT t.id, t.subject, coalesce(nullif(u.name,''), u.email, ''), to_char(t.updated_at,'YYYY-MM-DD HH24:MI')
		 FROM tickets t
		 LEFT JOIN users u ON u.id=t.user_id
		 WHERE t.status<>'closed' ORDER BY t.updated_at DESC LIMIT 10`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var subject, user, t string
			if err := rows.Scan(&id, &subject, &user, &t); err != nil {
				return nil, err
			}
			return map[string]any{
				"type": "ticket", "title": "工单 · " + subject,
				"time": t, "link": "/tickets",
			}, nil
		},
	)

	// 公告 → 通知
	collect(&notices,
		`SELECT id, title, to_char(created_at,'YYYY-MM-DD') FROM announcements
		 WHERE hidden=false ORDER BY pinned DESC, id DESC LIMIT 10`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var title, t string
			if err := rows.Scan(&id, &title, &t); err != nil {
				return nil, err
			}
			return map[string]any{
				"type": "notice", "title": title, "time": t, "link": "/announcements",
			}, nil
		},
	)

	writeJSON(w, map[string]any{
		"ok": 1, "pending": pending,
		"todos": todos, "messages": messages, "notices": notices,
	})
}
