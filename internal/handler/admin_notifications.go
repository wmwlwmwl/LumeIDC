package handler

import (
	"database/sql"
	"log"
	"net/http"
	"strconv"

	"lumeidc/internal/middleware"
	"lumeidc/internal/plugin"
)

// noticeFulfillmentOp 由服务的过渡态推断失败的操作类型；开通不设过渡态，故为兜底值。
func noticeFulfillmentOp(state string) string {
	switch state {
	case "renew_pending":
		return "续费"
	case "upgrading":
		return "升降级"
	default:
		return "开通"
	}
}

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
				// 直接进该条申请的审核页（admin-verification-detail 支持 :id）
				"time": t, "link": "/verifications/" + strconv.FormatInt(id, 10),
			}, nil
		},
	)

	// 履约失败（开通/续费/升降配）→ 待办
	// ponytail: 判据用 services.provision_error 而不是 fulfillment_jobs.status——
	// 管理员退款不会改任务状态（只写 checkpoint 并清服务状态），按任务状态统计会留下
	// 永远清不掉的红点；provision_error 只在失败待处理时非空，重试成功或退款后会被清空，
	// 条目随处置自动消失。
	//
	// 必须同时限定 status<3（已删除的服务不再展示）：删除服务不会清空 provision_error，
	// 而「服务实例」列表页有 status<3 过滤——只按 provision_error 统计会出现
	// 「待办里有、点进去却搜不到」的幽灵条目，红点也永远清不掉。
	// 两处过滤条件保持一致，条目跳过去才一定能落到那一行。
	// 条件直接写 provision_error<>''（该列 NOT NULL DEFAULT ''）：包一层 coalesce 会让
	// 优化器无法与部分索引谓词匹配，071 的索引就白建了。
	pending += count(`SELECT count(*) FROM services WHERE provision_error<>'' AND status<3`)
	collect(&todos,
		`SELECT s.id, coalesce(nullif(s.name,''),'服务 #'||s.id), coalesce(s.transition_state,''),
		        left(coalesce(s.provision_error,''),80),
		        to_char(coalesce(j.last_at, s.created_at),'YYYY-MM-DD HH24:MI')
		 FROM services s
		 LEFT JOIN LATERAL (SELECT max(updated_at) AS last_at FROM fulfillment_jobs WHERE service_id=s.id) j ON true
		 WHERE s.provision_error<>'' AND s.status<3
		 ORDER BY coalesce(j.last_at, s.created_at) DESC LIMIT 10`,
		func(rows *sql.Rows) (map[string]any, error) {
			var id int64
			var svc, state, reason, t string
			if err := rows.Scan(&id, &svc, &state, &reason, &t); err != nil {
				return nil, err
			}
			title := noticeFulfillmentOp(state) + "失败 · " + svc + "（#" + strconv.FormatInt(id, 10) + "）"
			if reason != "" {
				title += "：" + reason
			}
			return map[string]any{
				"type": "fulfillment", "title": title,
				// 跳服务实例列表并按编号定位：重试开通/续费/升降级与退款都在列表行的操作列，
				// 而 /services/{id} 是复用的用户端详情页，那里没有任何运维按钮。
				"time": t, "link": "/services?q=" + strconv.FormatInt(id, 10),
			}, nil
		},
	)

	// 工单（未关闭）→ 消息（工单由 tickets 插件拥有；禁用时铃铛不含工单条目）
	if plugin.Enabled("tickets") {
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
					"time": t, "link": "/plugin/tickets",
				}, nil
			},
		)
	}

	// 公告 → 通知（公告由 announcement 插件拥有；插件禁用时铃铛不含公告条目）
	if plugin.Enabled("announcement") {
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
	}

	writeJSON(w, map[string]any{
		"ok": 1, "pending": pending,
		"todos": todos, "messages": messages, "notices": notices,
	})
}
