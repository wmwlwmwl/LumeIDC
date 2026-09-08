package handler

import (
	"net/http"
	"strconv"
)

// Pager 服务端分页视图数据（列表页模板渲染用）。
// Base 为列表路径前缀；PrevURL/NextURL 为已带全部查询参数的跳转地址。
type Pager struct {
	Page    int   // 当前页（1 起）
	Per     int   // 每页条数
	Total   int64 // 总条数
	Pages   int   // 总页数
	Base    string
	Q       string // 当前关键词（用于搜索框回显）
	PrevURL string
	NextURL string
}

// pageParam 读取 ?p= 页码，非法/越界时钳制到 [1, 1e6]。
func pageParam(r *http.Request) int {
	raw := r.URL.Query().Get("p")
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}
	if n > 1000000 {
		return 1000000
	}
	return n
}

// pagerFor 依据总数与每页大小构造视图数据（自动钳制当前页）。
func pagerFor(r *http.Request, base string, per int, total int64) Pager {
	if per < 1 {
		per = 25
	}
	pages := int((total + int64(per) - 1) / int64(per))
	if pages < 1 {
		pages = 1
	}
	page := pageParam(r)
	if page > pages {
		page = pages
	}
	prev, next := page-1, page+1
	if prev < 1 {
		prev = 1
	}
	if next > pages {
		next = pages
	}
	return Pager{
		Page: page, Per: per, Total: total, Pages: pages, Base: base,
		PrevURL: pageURLFor(r, base, prev),
		NextURL: pageURLFor(r, base, next),
	}
}

// pageURLFor 基于当前请求查询参数生成第 page 页地址（仅替换 p）。
func pageURLFor(r *http.Request, base string, page int) string {
	q := r.URL.Query()
	q.Set("p", strconv.Itoa(page))
	if enc := q.Encode(); enc != "" {
		return base + "?" + enc
	}
	return base
}
