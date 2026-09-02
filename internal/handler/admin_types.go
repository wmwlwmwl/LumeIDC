package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lumeidc/internal/repo"
)

type typeRow struct {
	ID           int64
	ParentID     int64
	Name         string
	Description  string
	Sort         int
	Hidden       bool
	ProductCount int
	Children     []typeRow
}

// buildTypeRows 扁平分类组装为两级树（一级 + Children），并挂直挂产品数。

func buildTypeRows(types []repo.ProductType, counts map[int64]int) []typeRow {
	var firsts []typeRow
	idx := map[int64]int{}
	for _, t := range types {
		if t.ParentID == 0 {
			idx[t.ID] = len(firsts)
			firsts = append(firsts, typeRow{
				ID: t.ID, ParentID: 0, Name: t.Name, Description: t.Description,
				Sort: t.Sort, Hidden: t.Hidden, ProductCount: counts[t.ID],
			})
		}
	}
	for _, t := range types {
		if t.ParentID == 0 {
			continue
		}
		if i, ok := idx[t.ParentID]; ok {
			firsts[i].Children = append(firsts[i].Children, typeRow{
				ID: t.ID, ParentID: t.ParentID, Name: t.Name, Description: t.Description,
				Sort: t.Sort, Hidden: t.Hidden, ProductCount: counts[t.ID],
			})
		}
	}
	return firsts
}

func (m *AdminManage) TypesList(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	list, err := m.Products.ListTypes(r.Context())
	if err != nil {
		http.Error(w, "查询失败", 500)
		return
	}
	counts, _ := m.Products.TypeProductCounts(r.Context())
	m.renderAdmin(w, "admin_types.html", AdminData{
		Rows: buildTypeRows(list, counts), CSRF: m.adminCSRF(w, r), Error: r.URL.Query().Get("err"),
	})
}

// typeRedirect 分类错误跳转（err 统一转义，防中文消息破链接）。

func typeRedirect(w http.ResponseWriter, r *http.Request, err error) {
	http.Redirect(w, r, "/admin/types?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
}

func (m *AdminManage) TypeSave(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		typeRedirect(w, r, errors.New("名称必填"))
		return
	}
	desc := strings.TrimSpace(r.PostFormValue("description"))
	sort, _ := strconv.Atoi(r.PostFormValue("sort"))
	parentID, _ := strconv.ParseInt(r.PostFormValue("parent_id"), 10, 64)
	hidden := r.PostFormValue("hidden") != ""
	var id int64
	if idStr := r.PostFormValue("id"); idStr != "" {
		id, _ = strconv.ParseInt(idStr, 10, 64)
	}
	// 父分类校验：必须存在且为一级，且不能是自己（防自环）
	if parentID != 0 {
		types, err := m.Products.ListTypes(r.Context())
		if err != nil {
			typeRedirect(w, r, errors.New("查询失败"))
			return
		}
		parent, found := repo.FindType(types, parentID)
		if !found {
			typeRedirect(w, r, repo.ErrTypeNotFound)
			return
		}
		if parent.ParentID != 0 {
			typeRedirect(w, r, errors.New("仅支持两级分类，父分类必须为一级分类"))
			return
		}
		if parentID == id {
			typeRedirect(w, r, errors.New("不能将自己设为父分类"))
			return
		}
	}
	var err error
	if id == 0 {
		_, err = m.Products.CreateType(r.Context(), name, desc, sort, parentID, hidden)
	} else {
		err = m.Products.UpdateType(r.Context(), id, name, desc, sort, parentID, hidden)
	}
	if err != nil {
		typeRedirect(w, r, err)
		return
	}
	if id == 0 {
		m.audit(r, "type_create", "type", parentID, name)
	} else {
		m.audit(r, "type_update", "type", id, name)
	}
	http.Redirect(w, r, "/admin/types", http.StatusSeeOther)
}

// TypeMoveProducts 整组移动产品到其他分类（清空后才能删除，对齐 ZJMF）。

func (m *AdminManage) TypeMoveProducts(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	from, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	to, _ := strconv.ParseInt(r.PostFormValue("target_id"), 10, 64)
	types, err := m.Products.ListTypes(r.Context())
	if err != nil {
		typeRedirect(w, r, errors.New("查询失败"))
		return
	}
	if _, ok := repo.FindType(types, from); !ok {
		typeRedirect(w, r, repo.ErrTypeNotFound)
		return
	}
	if target, ok := repo.FindType(types, to); !ok || target.ParentID == 0 || from == to {
		typeRedirect(w, r, errors.New("目标分类无效"))
		return
	}
	n, err := m.Products.MoveTypeProducts(r.Context(), from, to)
	if err != nil {
		typeRedirect(w, r, err)
		return
	}
	m.audit(r, "type_move_products", "type", from, fmt.Sprintf("移动 %d 个产品到分类 %d", n, to))
	http.Redirect(w, r, "/admin/types?msg="+url.QueryEscape(fmt.Sprintf("已移动 %d 个产品", n)), http.StatusSeeOther)
}

func (m *AdminManage) TypeDelete(w http.ResponseWriter, r *http.Request) {
	if !m.require(w, r) {
		return
	}
	if !m.requireCSRF(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := m.Products.DeleteType(r.Context(), id); err != nil {
		typeRedirect(w, r, err)
		return
	}
	m.audit(r, "type_delete", "type", id, "")
	http.Redirect(w, r, "/admin/types", http.StatusSeeOther)
}
