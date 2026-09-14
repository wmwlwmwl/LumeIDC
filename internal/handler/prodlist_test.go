package handler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"lumeidc/internal/repo"
)

func TestProductsListData(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip()
	}
	d, _ := sql.Open("pgx", dsn)
	defer d.Close()
	p := repo.NewProducts(d)
	list, err := p.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	types, _ := p.ListTypes(context.Background())
	t.Logf("products=%d types=%d", len(list), len(types))
	psID, err := p.DefaultPricesetID(context.Background())
	t.Logf("priceset=%d err=%v", psID, err)
	for _, pr := range list {
		price, err := p.Price(context.Background(), pr.ID, psID)
		t.Logf("prod %d price=%+v err=%v", pr.ID, price, err)
	}
}

// 仅使用内存驱动验证实际 handler/repo 查询路径，不读取 DSN、不连接数据库。
type prodListQuery struct {
	sql  string
	args []any
	cols string
	rows [][]driver.Value
	err  error
}

type prodListDB struct {
	t     *testing.T
	steps []prodListQuery
	calls int
}

func (d *prodListDB) Connect(context.Context) (driver.Conn, error) { return d, nil }
func (d *prodListDB) Driver() driver.Driver                        { return d }
func (d *prodListDB) Open(string) (driver.Conn, error) {
	return nil, errors.New("测试禁止打开真实数据库")
}
func (d *prodListDB) Close() error { return nil }
func (d *prodListDB) Begin() (driver.Tx, error) {
	return nil, errors.New("测试不支持事务")
}
func (d *prodListDB) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("测试不支持预编译")
}
func (d *prodListDB) CheckNamedValue(*driver.NamedValue) error { return nil }
func (d *prodListDB) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	d.t.Helper()
	d.calls++
	if d.calls > len(d.steps) {
		d.t.Fatalf("出现额外查询（第 %d 次）: %s", d.calls, query)
	}
	step := d.steps[d.calls-1]
	var values []any
	for _, arg := range args {
		values = append(values, arg.Value)
	}
	if !strings.Contains(query, step.sql) || !reflect.DeepEqual(values, step.args) {
		d.t.Fatalf("第 %d 次查询不符: %s，参数=%v，期望包含 %s，参数=%v", d.calls, query, values, step.sql, step.args)
	}
	if step.err != nil {
		return nil, step.err
	}
	return &prodListRows{cols: strings.Split(step.cols, ","), rows: step.rows}, nil
}

type prodListRows struct {
	cols []string
	rows [][]driver.Value
}

func (r *prodListRows) Columns() []string { return r.cols }
func (r *prodListRows) Close() error      { return nil }
func (r *prodListRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}

func prodListSteps(n int) []prodListQuery {
	ids := make([]int64, n)
	steps := []prodListQuery{
		{sql: "FROM product_types ORDER BY sort,id", cols: "id,name,description,sort,parent_id,hidden", rows: [][]driver.Value{
			{int64(1), "云", "", int64(0), int64(0), false},
			{int64(2), "国内", "分类说明", int64(0), int64(1), false},
		}},
		{sql: "WHERE hidden=false AND upstream_offline_reason='' AND type_id = ANY($1) ORDER BY id", args: []any{[]int64{2}}, cols: "id,type_id,server_id,upstream_pid,upstream_cycle,name,description,stock,hidden,profit_type,profit_value,requires_identity,upstream_offline_reason"},
		{sql: "SELECT min(id) FROM pricesets", cols: "id", rows: [][]driver.Value{{int64(7)}}},
		{sql: "FROM product_prices WHERE priceset_id=$1 AND product_id = ANY($2)", args: []any{int64(7), ids}, cols: "product_id,monthly,quarterly,yearly"},
		{sql: "SELECT id, configoption FROM products WHERE id = ANY($1)", args: []any{ids}, cols: "id,configoption"},
		{sql: "FROM products pr LEFT JOIN servers s ON s.id=pr.server_id", args: []any{ids}, cols: "id,profit_type,profit_value"},
	}
	for i := range n {
		id := int64(i + 10)
		ids[i] = id
		monthly, config := "100", `[]`
		pt, pv, st, sv := int64(0), float64(0), int64(0), float64(10)
		switch i % 10 {
		case 0: // 服务器百分比利润：110
		case 1: // 商品固定利润优先：107
			pt, pv = 1, 7
		case 2: // 商品百分比利润优先于服务器固定利润：120
			pv, st, sv = 20, 1, 50
		case 3: // 配置计价 + 最低档初装费 + 服务器固定利润：35
			monthly, st, sv = "0", 1, 5
			config = `[{"field":"cpu","option_mode":"select","sub":[{"pricing":{"monthly":40},"setup":{"monthly":1}},{"pricing":{"monthly":25},"setup":{"monthly":5}}]}]`
		case 4: // 负商品利润仍回退，服务器类型和值分别钳零：110
			pv, st = -1, -1
		case 5: // 服务器负利润钳零：100
			st, sv = 1, -1
		case 6: // 未绑定服务器：100
			st, sv = 0, 0
		case 7: // 缺价格，即使有配置和利润也显示“-”
		case 8: // 损坏配置按无配置处理：110
			config = `{`
		case 9: // 非法基础价按 0，无配置：0
			monthly = "无效"
		}
		var sid driver.Value = int64(3)
		if i%10 == 6 {
			sid = nil
		}
		steps[1].rows = append(steps[1].rows, []driver.Value{id, int64(2), sid, int64(0), "monthly", fmt.Sprintf("商品%d", id), "<b>配置说明</b>", int64(-1), false, pt, pv, false, ""})
		if i%10 != 7 {
			steps[3].rows = append(steps[3].rows, []driver.Value{id, monthly, "0", "0"})
		}
		steps[4].rows = append(steps[4].rows, []driver.Value{id, []byte(config)})
		steps[5].rows = append(steps[5].rows, []driver.Value{id, st, sv})
	}
	if n == 0 {
		return steps[:3]
	}
	return steps
}

func TestProductListPageBatchQueries(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
		fail int // 1 起计的查询编号，0 表示全部成功
	}{
		{"空分类", 0, 0}, {"单商品", 1, 0}, {"百商品", 100, 0},
		{"分类查询失败", 10, 1}, {"商品查询失败", 10, 2},
		{"价格组查询失败", 10, 3}, {"价格查询失败", 10, 4},
		{"配置查询失败", 10, 5}, {"利润查询失败", 10, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := prodListSteps(tc.n)
			if tc.fail > 0 {
				steps[tc.fail-1].err = errors.New("模拟查询失败")
				if tc.fail <= 2 {
					steps = steps[:tc.fail]
				}
				if tc.fail == 3 {
					steps[3].args[0] = int64(0)
					steps[3].rows = nil
				}
			}
			driverDB := &prodListDB{t: t, steps: steps}
			db := sql.OpenDB(driverDB)
			t.Cleanup(func() { db.Close() })
			h := &Pages{Products: repo.NewProducts(db)}
			w := httptest.NewRecorder()
			h.cart(w, httptest.NewRequest(http.MethodGet, "/cart?fid=1&gid=2", nil))
			if driverDB.calls != len(steps) {
				t.Fatalf("查询数=%d，期望 %d", driverDB.calls, len(steps))
			}
			if tc.fail == 1 || tc.fail == 2 {
				if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != "读取产品失败" {
					t.Fatalf("主数据错误响应不符: %d %s", w.Code, w.Body.String())
				}
				return
			}
			var got struct {
				Products []struct {
					ID      int64
					Name    string
					Desc    string
					Monthly string
					Stock   int
				}
				Fid string
				Gid string
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
				t.Fatalf("响应解码失败: 状态=%d，错误=%v，正文=%s", w.Code, err, w.Body.String())
			}
			if got.Fid != "1" || got.Gid != "2" || len(got.Products) != tc.n {
				t.Fatalf("分类或商品数量不符: %+v", got)
			}
			want := []string{"110.00", "107.00", "120.00", "35.00", "110.00", "100.00", "100.00", "-", "110.00", "0.00"}
			if tc.fail == 5 {
				want[3] = "5.00"
			}
			if tc.fail == 6 {
				want[0], want[3], want[4], want[8] = "100.00", "30.00", "100.00", "100.00"
			}
			for i, p := range got.Products {
				price := want[i%len(want)]
				if tc.fail == 3 || tc.fail == 4 {
					price = "-"
				}
				if p.ID != int64(i+10) || p.Name != fmt.Sprintf("商品%d", p.ID) || p.Desc != "<b>配置说明</b>" || p.Stock != -1 || p.Monthly != price {
					t.Errorf("第 %d 个商品不符: %+v，期望价格=%s", i, p, price)
				}
			}
			t.Logf("商品数=%d，内存驱动记录查询数=%d", tc.n, driverDB.calls)
		})
	}
}
