package repo

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestTrendsWindowBoundary 锁定趋势聚合的日期下界边界。
//
// 聚合 CTE 必须自带与 generate_series 相同的下界（否则每次刷新全表聚合），
// 且下界必须是 >=：写成 > 会把「正好落在窗口首日 00:00」的记录漏掉，
// 仪表盘首日数据凭空少一截。这里用「插入前后同一天计数差 1」判定，
// 不受共享测试库里既有数据量影响。
func TestTrendsWindowBoundary(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("no dsn")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	const days = 7
	st := &Stats{db: d}

	first := func() int64 {
		t.Helper()
		points, err := st.Trends(ctx, days)
		if err != nil {
			t.Fatal(err)
		}
		if len(points) != days {
			t.Fatalf("应返回 %d 个趋势点，实得 %d", days, len(points))
		}
		var want string
		if err := d.QueryRowContext(ctx,
			`SELECT to_char((current_date - ($1::int - 1))::date,'MM-DD')`, days).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if points[0].Date != want {
			t.Fatalf("首个趋势点应为窗口首日 %s，实得 %s", want, points[0].Date)
		}
		return points[0].Users
	}

	before := first()
	// 正好落在窗口首日 00:00（session 时区的当天零点）。
	var uid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash,created_at)
		 VALUES($1,'测试',(current_date - ($2::int - 1))::timestamptz) RETURNING id`,
		"stats-trend-"+time.Now().Format("150405.000000000")+"@example.invalid", days).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, uid); err != nil {
			t.Errorf("清理测试用户失败: %v", err)
		}
	}()

	if after := first(); after != before+1 {
		t.Fatalf("窗口首日 00:00 注册的用户必须计入趋势：插入前 %d，插入后 %d", before, after)
	}

	// 窗口外一天（首日的前一天 23:59:59）不得计入任何趋势点。
	var oldID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash,created_at)
		 VALUES($1,'测试',(current_date - $2::int)::timestamptz + interval '23:59:59') RETURNING id`,
		"stats-trend-out-"+time.Now().Format("150405.000000000")+"@example.invalid", days).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, oldID); err != nil {
			t.Errorf("清理测试用户失败: %v", err)
		}
	}()
	if after := first(); after != before+1 {
		t.Fatalf("窗口外记录不应计入趋势：期望仍为 %d，实得 %d", before+1, after)
	}
}
