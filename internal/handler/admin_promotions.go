package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lumeidc/internal/repo"
	"lumeidc/internal/service"
)

// adminPromotions GET /admin/promotions — 活动列表。
func (a *Admin) adminPromotions(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	list, err := a.Promotions.List(r.Context())
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "type": p.Type,
			"starts_at": p.StartsAt.Format("2006-01-02 15:04"),
			"ends_at":   p.EndsAt.Format("2006-01-02 15:04"),
			"enabled":   p.Enabled, "limit_per_user": p.LimitPerUser,
			"status": p.Status(),
		})
	}
	writeJSON(w, map[string]any{"ok": 1, "list": out})
}

// adminPromotionDetail GET /admin/promotions/{id} — 活动详情（含商品绑定）。
func (a *Admin) adminPromotionDetail(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, err := a.Promotions.Get(r.Context(), id)
	if err != nil {
		jsonStatus(w, r, 404, "活动不存在")
		return
	}
	products, err := a.Promotions.ListProducts(r.Context(), id)
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	pl := make([]map[string]any, 0, len(products))
	for _, pp := range products {
		item := map[string]any{
			"product_id": pp.ProductID, "rules": pp.Rules,
		}
		if pp.PricesetID.Valid {
			item["priceset_id"] = pp.PricesetID.Int64
		}
		if pp.Cycle.Valid {
			item["cycle"] = pp.Cycle.String
		}
		pl = append(pl, item)
	}
	writeJSON(w, map[string]any{
		"ok": 1,
		"promotion": map[string]any{
			"id": p.ID, "name": p.Name, "description": p.Description, "type": p.Type,
			"banner": p.Banner, "notice": p.Notice, "rules_text": p.RulesText,
			"starts_at": p.StartsAt.Format("2006-01-02T15:04"),
			"ends_at":   p.EndsAt.Format("2006-01-02T15:04"),
			"enabled":   p.Enabled, "limit_per_user": p.LimitPerUser,
		},
		"products": pl,
	})
}

// adminPromotionSave POST /admin/promotions/save（新建）或 /admin/promotions/{id}/save（更新）。
func (a *Admin) adminPromotionSave(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	vals := jsonVals(r)
	fv := func(k string) string {
		if vals != nil {
			return vals[k]
		}
		return r.PostFormValue(k)
	}
	idStr := r.PathValue("id")
	var p repo.Promotion
	if idStr != "" {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		existing, err := a.Promotions.Get(r.Context(), id)
		if err != nil {
			jsonStatus(w, r, 404, "活动不存在")
			return
		}
		p = *existing
	}
	p.Name = strings.TrimSpace(fv("name"))
	p.Description = fv("description")
	p.Type = fv("type")
	p.Banner = fv("banner")
	p.Notice = fv("notice")
	p.RulesText = fv("rules_text")
	p.Enabled = fv("enabled") == "1" || fv("enabled") == "true"
	if v, err := strconv.Atoi(fv("limit_per_user")); err == nil {
		p.LimitPerUser = v
	}
	if v, err := time.Parse("2006-01-02T15:04", fv("starts_at")); err == nil {
		p.StartsAt = v
	}
	if v, err := time.Parse("2006-01-02T15:04", fv("ends_at")); err == nil {
		p.EndsAt = v
	}
	if p.Name == "" {
		jsonStatus(w, r, 400, "活动名称不能为空")
		return
	}
	if err := service.ValidatePromotionType(p.Type); err != nil {
		jsonStatus(w, r, 400, err.Error())
		return
	}
	// 商品绑定
	var products []repo.PromotionProduct
	if raw := fv("products"); raw != "" {
		var arr []map[string]any
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			for _, item := range arr {
				pid, _ := item["product_id"].(float64)
				if pid == 0 {
					continue
				}
				pp := repo.PromotionProduct{
					ProductID: int64(pid),
				}
				if v, ok := item["priceset_id"].(float64); ok && v > 0 {
					pp.PricesetID = sql.NullInt64{Int64: int64(v), Valid: true}
				}
				if v, ok := item["cycle"].(string); ok && v != "" {
					pp.Cycle = sql.NullString{String: v, Valid: true}
				}
				if rules, ok := item["rules"].(map[string]any); ok {
					if b, err := json.Marshal(rules); err == nil {
						pp.Rules = b
					}
				}
				products = append(products, pp)
			}
		}
	}

	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		jsonStatus(w, r, 500, "事务开启失败")
		return
	}
	defer tx.Rollback()
	if idStr == "" {
		newID, err := a.Promotions.CreateTx(r.Context(), tx, &p)
		if err != nil {
			jsonStatus(w, r, 500, "创建失败: "+err.Error())
			return
		}
		p.ID = newID
	} else {
		if err := a.Promotions.UpdateTx(r.Context(), tx, &p); err != nil {
			jsonStatus(w, r, 500, "更新失败: "+err.Error())
			return
		}
	}
	if err := a.Promotions.SaveProducts(r.Context(), tx, p.ID, products); err != nil {
		jsonStatus(w, r, 500, "保存商品失败: "+err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		jsonStatus(w, r, 500, "提交失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "id": p.ID, "msg": "保存成功"})
}

// adminPromotionDelete POST /admin/promotions/{id}/delete。
func (a *Admin) adminPromotionDelete(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.Promotions.Delete(r.Context(), id); err != nil {
		jsonStatus(w, r, 500, "删除失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "msg": "已删除"})
}

// adminPromotionToggle POST /admin/promotions/{id}/toggle。
func (a *Admin) adminPromotionToggle(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	vals := jsonVals(r)
	enabled := vals["enabled"] == "1" || vals["enabled"] == "true" || r.PostFormValue("enabled") == "1"
	if err := a.Promotions.Toggle(r.Context(), id, enabled); err != nil {
		jsonStatus(w, r, 500, "操作失败")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "msg": "已更新"})
}

// adminPromotionStats GET /admin/promotions/{id}/stats — 数据看板。
func (a *Admin) adminPromotionStats(w http.ResponseWriter, r *http.Request) {
	if !a.require(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	views, claimed, orders, paidAmount, err := a.Promotions.GetStats(r.Context(), id)
	if err != nil {
		jsonStatus(w, r, 500, "查询失败")
		return
	}
	writeJSON(w, map[string]any{
		"ok": 1,
		"stats": map[string]any{
			"views": views, "claimed": claimed, "orders": orders, "paid_amount": paidAmount,
		},
	})
}
