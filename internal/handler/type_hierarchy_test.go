package handler

import (
	"context"
	"database/sql"
	"errors"
	"html/template"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"lumeidc/internal/repo"
)

func mkTestTypes() []repo.ProductType {
	return []repo.ProductType{
		{ID: 1, Name: "云", Sort: 1},
		{ID: 2, Name: "国内", Sort: 1, ParentID: 1},
		{ID: 3, Name: "海外", Sort: 2, ParentID: 1},
		{ID: 4, Name: "域名", Sort: 2},
		{ID: 5, Name: "隐藏子", Sort: 3, ParentID: 1, Hidden: true},
		{ID: 6, Name: "隐藏父", Sort: 3, Hidden: true},
		{ID: 7, Name: "隐藏父的子", Sort: 1, ParentID: 6},
	}
}

func TestBuildTypeNav(t *testing.T) {
	nav := buildTypeNav(mkTestTypes())
	if len(nav) != 2 { // 云、域名；隐藏父被过滤
		t.Fatalf("一级导航数=%d, 期望 2: %+v", len(nav), nav)
	}
	if nav[0].Name != "云" || len(nav[0].Children) != 2 { // 隐藏子被过滤
		t.Fatalf("云 children=%+v", nav[0].Children)
	}
	if nav[1].Name != "域名" || len(nav[1].Children) != 0 {
		t.Fatalf("域名 children=%+v", nav[1].Children)
	}
}

func TestTypeVisible(t *testing.T) {
	types := mkTestTypes()
	cases := []struct {
		id   int64
		want bool
	}{
		{1, true}, {2, true}, {4, true},
		{5, false}, {6, false},
		{7, false}, // 父隐藏则子不可见（级联）
	}
	for _, c := range cases {
		tt, ok := repo.FindType(types, c.id)
		if !ok {
			t.Fatalf("id=%d 不存在", c.id)
		}
		if got := typeVisible(tt, types); got != c.want {
			t.Errorf("id=%d visible=%v, 期望 %v", c.id, got, c.want)
		}
	}
}

func TestBuildTypeRows(t *testing.T) {
	counts := map[int64]int{1: 2, 2: 3}
	rows := buildTypeRows(mkTestTypes(), counts)
	if len(rows) != 3 { // 云、域名、隐藏父（后台不过滤隐藏）
		t.Fatalf("一级行数=%d, 期望 3", len(rows))
	}
	if len(rows[0].Children) != 3 { // 国内/海外/隐藏子（后台全显示）
		t.Fatalf("云 children=%d, 期望 3", len(rows[0].Children))
	}
	if rows[0].ProductCount != 2 || rows[0].Children[0].ProductCount != 3 {
		t.Fatalf("产品数挂载错误: %+v", rows[0])
	}
}

func TestFindType(t *testing.T) {
	if _, ok := repo.FindType(mkTestTypes(), 99); ok {
		t.Fatal("id=99 应不存在")
	}
	if tt, ok := repo.FindType(mkTestTypes(), 3); !ok || tt.Name != "海外" {
		t.Fatalf("id=3 查找失败: %+v", tt)
	}
}

// TestTemplatesParse 组合解析全部后台/前台模板，捕获语法与 end 失配错误。
func TestTemplatesParse(t *testing.T) {
	entries, err := adminFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "admin_") || !strings.HasSuffix(name, ".html") {
			continue
		}
		if _, err := template.ParseFS(adminFS, "templates/admin.html", "templates/"+name); err != nil {
			t.Errorf("解析 %s: %v", name, err)
		}
	}
	if _, err := template.ParseFS(siteFS, "templates/site.html", "templates/products.html"); err != nil {
		t.Errorf("解析 products.html: %v", err)
	}
	// 带数据执行：捕获运行期字段缺失（如 typeRow 漏 ParentID 会让页面渲染中断）
	tpl, err := template.ParseFS(adminFS, "templates/admin.html", "templates/admin_types.html")
	if err != nil {
		t.Fatal(err)
	}
	data := AdminData{Rows: []typeRow{{
		ID: 1, Name: "云", Sort: 1, ProductCount: 2,
		Children: []typeRow{{ID: 2, ParentID: 1, Name: "国内", Sort: 1, ProductCount: 3, Hidden: true}},
	}}}
	var b strings.Builder
	if err := tpl.ExecuteTemplate(&b, "admin", data); err != nil {
		t.Fatalf("执行 admin_types.html: %v", err)
	}
	for _, want := range []string{"新增分类", "移动产品", "删除"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("渲染结果缺少关键内容: %s", want)
		}
	}
	// 导入页：分类下拉（一级清单）
	tpl2, err := template.ParseFS(adminFS, "templates/admin.html", "templates/admin_catalog.html")
	if err != nil {
		t.Fatal(err)
	}
	data2 := AdminData{Types: []repo.ProductType{{ID: 1, Name: "云产品"}}, Rows: []catalogRow{{PID: 1, A: "测试商品"}}}
	b.Reset()
	if err := tpl2.ExecuteTemplate(&b, "admin", data2); err != nil {
		t.Fatalf("执行 admin_catalog.html: %v", err)
	}
	for _, want := range []string{"parent_id", "请选择目标一级分类", "建为「云产品」下的二级分类"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("导入页渲染缺少: %s", want)
		}
	}
}

// TestEnsureTypeHierarchy 需要真实 PG：上游导入分组名两级展开与幂等匹配。
func TestEnsureTypeHierarchy(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_DATABASE_DSN，跳过")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	m := &AdminManage{Products: &repo.Products{DB: d}}

	// 两级分组名（parentID=0）→ 建一级+二级，产品挂二级
	cid, err := m.ensureType(ctx, "测试云/测试国内", 0)
	if err != nil {
		t.Fatal(err)
	}
	types, _ := m.Products.ListTypes(ctx)
	c, ok := repo.FindType(types, cid)
	if !ok || c.ParentID == 0 || c.Name != "测试国内" {
		t.Fatalf("二级分类错误: %+v", c)
	}
	if p, ok := repo.FindType(types, c.ParentID); !ok || p.Name != "测试云" || p.ParentID != 0 {
		t.Fatalf("一级分类错误: %+v", p)
	}
	defer func() {
		d.ExecContext(ctx, `DELETE FROM products WHERE type_id IN ($1,$2)`, cid, c.ParentID)
		d.ExecContext(ctx, `DELETE FROM product_types WHERE id IN ($1,$2)`, cid, c.ParentID)
	}()

	// 同名二级挂对一级：另一一级下的同名二级不应命中
	otherFid, _ := m.Products.CreateType(ctx, "测试其他一级", "", 9998, 0, false)
	otherCid, _ := m.Products.CreateType(ctx, "测试国内", "", 9998, otherFid, false)
	defer func() {
		// otherFid 下所有测试二级（含 ensureType 新建的）一并清理
		d.ExecContext(ctx, `DELETE FROM products WHERE type_id IN (SELECT id FROM product_types WHERE parent_id=$1)`, otherFid)
		d.ExecContext(ctx, `DELETE FROM product_types WHERE parent_id=$1`, otherFid)
		d.ExecContext(ctx, `DELETE FROM products WHERE type_id IN ($1,$2)`, otherFid, otherCid)
		d.ExecContext(ctx, `DELETE FROM product_types WHERE id IN ($1,$2)`, otherFid, otherCid)
	}()
	if got, _ := m.ensureType(ctx, "测试云/测试国内", 0); got != cid {
		t.Fatalf("两级匹配应命中原二级 %d, 实际 %d", cid, got)
	}

	// 指定父分类（导入页选择一级）：单层上游分组名建为其下二级，幂等命中
	pid2, _ := m.ensureType(ctx, "国内高防加速CDN", otherFid)
	if got, _ := m.ensureType(ctx, "国内高防加速CDN", otherFid); got != pid2 {
		t.Fatalf("指定父分类应幂等命中 %d, 实际 %d", pid2, got)
	}
	pid3, _ := m.ensureType(ctx, "全新上游分组", otherFid)
	types, _ = m.Products.ListTypes(ctx)
	if n, ok := repo.FindType(types, pid3); !ok || n.ParentID != otherFid || n.Name != "全新上游分组" {
		t.Fatalf("指定父分类应建二级: %+v", n)
	}
	// 嵌套分组名 + 指定父分类：取末段建二级
	pid4, _ := m.ensureType(ctx, "忽略这段/末段分组", otherFid)
	types, _ = m.Products.ListTypes(ctx)
	if n, ok := repo.FindType(types, pid4); !ok || n.ParentID != otherFid || n.Name != "末段分组" {
		t.Fatalf("嵌套名应取末段建二级: %+v", n)
	}

	// 单名（parentID=0）→ 一级
	fid, err := m.ensureType(ctx, "测试单级分组", 0)
	if err != nil {
		t.Fatal(err)
	}
	types, _ = m.Products.ListTypes(ctx)
	if f, ok := repo.FindType(types, fid); !ok || f.ParentID != 0 || f.Name != "测试单级分组" {
		t.Fatalf("单名应建一级: %+v", f)
	}
	// 幂等：再次取同名返回同一 id
	if got, _ := m.ensureType(ctx, "测试单级分组", 0); got != fid {
		t.Fatalf("单名幂等失败: %d != %d", got, fid)
	}
	d.ExecContext(ctx, `DELETE FROM products WHERE type_id=$1`, fid)
	d.ExecContext(ctx, `DELETE FROM product_types WHERE id=$1`, fid)
}

// TestTypeHierarchyDB 需要真实 PG（已跑迁移）：层级 CRUD、删除保护、整组移动。
func TestTypeHierarchyDB(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_DATABASE_DSN，跳过")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	p := &repo.Products{DB: d}

	fid, err := p.CreateType(ctx, "测试一级", "", 9999, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	cid, err := p.CreateType(ctx, "测试子级", "", 9999, fid, false)
	if err != nil {
		t.Fatal(err)
	}
	var pid int64
	defer func() {
		p.Delete(ctx, pid) // 清理测试产品（此前误传分类 id，残留数据触发删除保护）
		p.DeleteType(ctx, cid)
		p.DeleteType(ctx, fid)
	}()

	// 建二级分类禁止：父必须是列表中存在的一级（handler 校验，repo 不拦）；此处验证数据落库正确
	types, err := p.ListTypes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child, ok := repo.FindType(types, cid)
	if !ok || child.ParentID != fid {
		t.Fatalf("子分类落库错误: %+v", child)
	}

	pid, err = p.Create(ctx, sql.NullInt64{Int64: cid, Valid: true}, "层级自测产品", "", -1)
	if err != nil {
		t.Fatal(err)
	}

	// 删除保护：有子分类/有产品均拒绝
	if err := p.DeleteType(ctx, fid); !errors.Is(err, repo.ErrTypeHasChildren) {
		t.Fatalf("删有子分类的一级: err=%v, 期望 ErrTypeHasChildren", err)
	}
	if err := p.DeleteType(ctx, cid); !errors.Is(err, repo.ErrTypeInUse) {
		t.Fatalf("删有产品的二级: err=%v, 期望 ErrTypeInUse", err)
	}

	// 整组移动
	n, err := p.MoveTypeProducts(ctx, cid, fid)
	if err != nil || n != 1 {
		t.Fatalf("移动产品: n=%d err=%v, 期望 n=1", n, err)
	}
	if err := p.DeleteType(ctx, cid); err != nil { // 移空后可删
		t.Fatalf("移空后删子分类: %v", err)
	}
	if err := p.DeleteType(ctx, fid); !errors.Is(err, repo.ErrTypeInUse) {
		t.Fatalf("删有产品的一级: err=%v, 期望 ErrTypeInUse", err)
	}

	// 按单分类查询：移动后的产品挂在一级下
	list, err := p.ListVisibleByTypes(ctx, []int64{fid})
	if err != nil || len(list) != 1 || list[0].ID != pid {
		t.Fatalf("按分类取产品: list=%+v err=%v", list, err)
	}
	if list, _ := p.ListVisibleByTypes(ctx, nil); len(list) != 0 {
		t.Fatalf("空分类集合应无产品, 实际 %d", len(list))
	}
}
