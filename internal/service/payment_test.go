package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"lumeidc/internal/repo"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("no dsn")
	}
	d, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// setupOnlinePayment 仅使用显式 TEST_DATABASE_DSN 指向的已迁移测试库。
func setupOnlinePayment(t *testing.T, balance string) (*Payment, *payFixture, int64) {
	t.Helper()
	d := testDB(t)
	f := setupPayFixture(t, d, "100.00", sql.NullString{}, 1)
	var invoiceID int64
	if err := d.QueryRow(`UPDATE invoices SET status=0 WHERE order_id=$1 RETURNING id`, f.orderID).Scan(&invoiceID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE users SET balance=$2 WHERE id=$1`, f.userID, balance); err != nil {
		t.Fatal(err)
	}
	return &Payment{db: d, Jobs: repo.NewFulfillmentJobs(d)}, f, invoiceID
}

func TestPrepareOnlineReplacesCredit(t *testing.T) {
	for _, tc := range []struct {
		name, balance, online, credit, fee, payable string
		useBalance, fullyCovered                    bool
	}{
		{"重复抵扣", "30.00", "70.00", "30.00", "1.40", "71.40", true, false},
		{"重复抵扣不能误判全额覆盖", "70.00", "30.00", "70.00", "0.60", "30.60", true, false},
		{"取消余额抵扣", "30.00", "100.00", "0.00", "2.00", "102.00", false, false},
		{"补足余额后全额覆盖", "100.00", "", "", "", "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, f, invID := setupOnlinePayment(t, "30.00")
			ctx := context.Background()
			first, err := p.PrepareOnline(ctx, invID, f.userID, "old", "0", true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`UPDATE users SET balance=$2::numeric-30 WHERE id=$1`, f.userID, tc.balance); err != nil {
				t.Fatal(err)
			}
			got, err := p.PrepareOnline(ctx, invID, f.userID, "new", "2", tc.useBalance)
			if err != nil {
				t.Fatal(err)
			}
			if got.FullyCovered != tc.fullyCovered || got.Online != tc.online || got.Credit != tc.credit || got.FeeAmount != tc.fee || got.Payable != tc.payable {
				t.Fatalf("重新发起金额不符：得到 %+v，期望在线本金 %s、抵扣 %s、应付 %s、全额覆盖 %v", got, tc.online, tc.credit, tc.payable, tc.fullyCovered)
			}
			var conserved bool
			if err := f.db.QueryRow(`SELECT u.balance+i.credit=$2::numeric FROM invoices i JOIN users u ON u.id=i.user_id WHERE i.id=$1`, invID, tc.balance).Scan(&conserved); err != nil || !conserved {
				t.Fatalf("余额与抵扣不守恒：%v", err)
			}
			if tc.fullyCovered {
				var pending int
				if err := f.db.QueryRow(`SELECT count(*) FROM payment_attempts WHERE invoice_id=$1 AND status=0`, invID).Scan(&pending); err != nil || pending != 0 {
					t.Fatalf("归还抵扣后不应保留可核销的旧尝试：待支付 %d，错误 %v", pending, err)
				}
				return
			}
			var valid bool
			if err := f.db.QueryRow(`SELECT a.amount-a.fee_amount+i.credit=i.amount AND a.status=0 AND old.status=2
				FROM invoices i JOIN payment_attempts a ON a.invoice_id=i.id JOIN payment_attempts old ON old.id=$3
				WHERE i.id=$1 AND a.id=$2`, invID, got.AttemptID, first.AttemptID).Scan(&valid); err != nil || !valid {
				t.Fatalf("本金核销或新旧尝试状态不符：%v", err)
			}
		})
	}
}

func TestReleaseInvoiceCreditAttemptOwnership(t *testing.T) {
	for _, name := range []string{"当前尝试及重复释放", "旧尝试迟到失败", "已支付后迟到失败", "已支付账单残留待支付尝试", "已取消账单", "已过期账单", "其他账单尝试", "零尝试", "不存在的尝试"} {
		t.Run(name, func(t *testing.T) {
			p, f, invID := setupOnlinePayment(t, "30.00")
			ctx := context.Background()
			prep, err := p.PrepareOnline(ctx, invID, f.userID, "test", "0", true)
			if err != nil {
				t.Fatal(err)
			}
			attemptID := prep.AttemptID
			switch name {
			case "旧尝试迟到失败":
				if _, err := p.PrepareOnline(ctx, invID, f.userID, "new", "0", true); err != nil {
					t.Fatal(err)
				}
			case "已支付后迟到失败":
				if _, err := f.db.Exec(`UPDATE orders SET status=0,service_id=$2 WHERE id=$1`, f.orderID, f.serviceID); err != nil {
					t.Fatal(err)
				}
				var no string
				if err := f.db.QueryRow(`SELECT no FROM invoices WHERE id=$1`, invID).Scan(&no); err != nil {
					t.Fatal(err)
				}
				if err := p.MarkPaid(ctx, no, no, "test", attemptID); err != nil {
					t.Fatal(err)
				}
			case "已支付账单残留待支付尝试", "已取消账单", "已过期账单":
				status := map[string]int{"已支付账单残留待支付尝试": 1, "已取消账单": 2, "已过期账单": 3}[name]
				if _, err := f.db.Exec(`UPDATE invoices SET status=$2 WHERE id=$1`, invID, status); err != nil {
					t.Fatal(err)
				}
			case "其他账单尝试":
				other, of, oid := setupOnlinePayment(t, "30.00")
				otherPrep, err := other.PrepareOnline(ctx, oid, of.userID, "test", "0", true)
				if err != nil {
					t.Fatal(err)
				}
				attemptID = otherPrep.AttemptID
			case "零尝试":
				attemptID = 0
			case "不存在的尝试":
				attemptID = -1
			}
			snapshot := func() string {
				t.Helper()
				var state string
				if err := f.db.QueryRow(`SELECT json_build_array(u.balance,i.credit,i.status,
					(SELECT json_agg(row(a.id,a.status) ORDER BY a.id) FROM payment_attempts a WHERE a.invoice_id=i.id OR a.id=$2),
					(SELECT count(*) FROM balance_logs WHERE user_id=u.id))::text
					FROM invoices i JOIN users u ON u.id=i.user_id WHERE i.id=$1`, invID, attemptID).Scan(&state); err != nil {
					t.Fatal(err)
				}
				return state
			}
			before := snapshot()
			if err := p.ReleaseInvoiceCredit(ctx, invID, attemptID); err != nil {
				t.Fatal(err)
			}
			if name == "当前尝试及重复释放" {
				var valid bool
				if err := f.db.QueryRow(`SELECT u.balance=30 AND i.credit=0 AND a.status=2
					FROM invoices i JOIN users u ON u.id=i.user_id JOIN payment_attempts a ON a.invoice_id=i.id
					WHERE i.id=$1 AND a.id=$2`, invID, attemptID).Scan(&valid); err != nil || !valid {
					t.Fatalf("当前尝试释放不完整：%v", err)
				}
				before = snapshot()
				if err := p.ReleaseInvoiceCredit(ctx, invID, attemptID); err != nil {
					t.Fatal(err)
				}
			}
			if after := snapshot(); after != before {
				t.Fatalf("无效或重复清理不应改变账务：原状态 %s，现状态 %s", before, after)
			}
		})
	}
}

// payFixture 一套「用户 + 产品 + 已付订单/账单 + 服务」的自建自删测试数据。
type payFixture struct {
	db        *sql.DB
	userID    int64
	productID int64
	orderID   int64
	serviceID int64
}

// setupPayFixture 建夹具。amount 为订单实付；snapshot 为 config_snapshot（nil 表示无快照）；
// serviceStatus 为服务状态（0 待开通）。
func setupPayFixture(t *testing.T, d *sql.DB, amount string, snapshot sql.NullString, serviceStatus int) *payFixture {
	t.Helper()
	ctx := context.Background()
	f := &payFixture{db: d}

	// 订单需要 priceset_id：复用库里最小的一条，避免额外造价格组。
	var psID int64
	if err := d.QueryRowContext(ctx, `SELECT min(id) FROM pricesets`).Scan(&psID); err != nil {
		t.Skipf("库中暂无价格组，跳过: %v", err)
	}

	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash) VALUES($1,'x') RETURNING id`,
		"paytest-"+time.Now().Format("150405.000000000")+"@example.invalid").Scan(&f.userID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO products(type_id,name,stock) VALUES(NULL,'单元测试-开通前退款',-1) RETURNING id`).
		Scan(&f.productID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO orders(user_id,product_id,priceset_id,cycle,amount,status,config_snapshot)
		 VALUES($1,$2,$3,'monthly',$4,1,$5) RETURNING id`,
		f.userID, f.productID, psID, amount, snapshot).Scan(&f.orderID); err != nil {
		t.Fatal(err)
	}
	// 已支付账单（gateway=balance 才会退回余额）。
	if _, err := d.ExecContext(ctx,
		`INSERT INTO invoices(no,user_id,order_id,amount,status,gateway,paid_at)
		 VALUES($1,$2,$3,$4,1,'balance',now())`,
		"TESTINV-"+time.Now().Format("150405.000000000"), f.userID, f.orderID, amount); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`INSERT INTO services(user_id,product_id,order_id,status) VALUES($1,$2,$3,$4) RETURNING id`,
		f.userID, f.productID, f.orderID, serviceStatus).Scan(&f.serviceID); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		for _, s := range []struct {
			q   string
			arg int64
		}{
			{`DELETE FROM refunds WHERE order_id=$1`, f.orderID},
			{`DELETE FROM balance_logs WHERE user_id=$1`, f.userID},
			{`DELETE FROM invoices WHERE order_id=$1`, f.orderID},
			{`DELETE FROM services WHERE id=$1`, f.serviceID},
			{`DELETE FROM orders WHERE id=$1`, f.orderID},
			{`DELETE FROM products WHERE id=$1`, f.productID},
			{`DELETE FROM users WHERE id=$1`, f.userID},
		} {
			if _, err := d.ExecContext(ctx, s.q, s.arg); err != nil {
				t.Errorf("清理测试数据失败(%s): %v", s.q, err)
			}
		}
	})
	return f
}

func (f *payFixture) balance(t *testing.T) float64 {
	t.Helper()
	var b float64
	if err := f.db.QueryRow(`SELECT balance::float8 FROM users WHERE id=$1`, f.userID).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *payFixture) orderStatus(t *testing.T) int {
	t.Helper()
	var s int
	if err := f.db.QueryRow(`SELECT status FROM orders WHERE id=$1`, f.orderID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *payFixture) serviceStatus(t *testing.T) int {
	t.Helper()
	var s int
	if err := f.db.QueryRow(`SELECT status FROM services WHERE id=$1`, f.serviceID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// 付款前复核：用户把账单挂着、上游改价（本地同步已落地）之后才付款，必须在收款前拦下来，
// 否则钱先收进来、开通时才发现上游贵了，只能转人工垫差价或退款。
func TestVerifyOrderPriceBeforePay(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	products := repo.NewProducts(d)
	p := &Payment{db: d, Products: products}
	psID, err := products.DefaultPricesetID(ctx)
	if err != nil {
		t.Skipf("库中暂无价格组，跳过: %v", err)
	}
	invoiceID := func(t *testing.T, orderID int64) int64 {
		t.Helper()
		var id int64
		if err := d.QueryRow(`SELECT id FROM invoices WHERE order_id=$1`, orderID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	setMonthly := func(t *testing.T, productID int64, monthly string) {
		t.Helper()
		if _, err := d.ExecContext(ctx,
			`INSERT INTO product_prices(product_id,priceset_id,monthly) VALUES($1,$2,$3)`,
			productID, psID, monthly); err != nil {
			t.Fatal(err)
		}
	}
	snap := func(total string) sql.NullString {
		return sql.NullString{String: `{"quote":{"total":` + total + `},"selection":{}}`, Valid: true}
	}

	t.Run("下单后涨价：拦下并提示重新下单", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", snap("10"), 0)
		setMonthly(t, f.productID, "20.00")
		err := p.VerifyOrderPriceBeforePay(ctx, invoiceID(t, f.orderID), f.userID)
		if !errors.Is(err, ErrUpstreamPriceChanged) {
			t.Fatalf("应拦下涨价，实得 %v", err)
		}
	})
	t.Run("价格未变/降价：放行", func(t *testing.T) {
		same := setupPayFixture(t, d, "11.00", snap("10"), 0)
		setMonthly(t, same.productID, "10.00")
		if err := p.VerifyOrderPriceBeforePay(ctx, invoiceID(t, same.orderID), same.userID); err != nil {
			t.Fatalf("原价应放行，实得 %v", err)
		}
		down := setupPayFixture(t, d, "11.00", snap("20"), 0)
		setMonthly(t, down.productID, "10.00")
		if err := p.VerifyOrderPriceBeforePay(ctx, invoiceID(t, down.orderID), down.userID); err != nil {
			t.Fatalf("降价应放行，实得 %v", err)
		}
	})
	t.Run("续费订单不拦（走开通前比价转人工）", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", snap("10"), 1)
		setMonthly(t, f.productID, "20.00")
		if _, err := d.ExecContext(ctx, `UPDATE orders SET service_id=$2 WHERE id=$1`, f.orderID, f.serviceID); err != nil {
			t.Fatal(err)
		}
		if err := p.VerifyOrderPriceBeforePay(ctx, invoiceID(t, f.orderID), f.userID); err != nil {
			t.Fatalf("续费订单不应在付款前拦价，实得 %v", err)
		}
	})
	t.Run("无快照老订单放行", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", sql.NullString{}, 0)
		setMonthly(t, f.productID, "20.00")
		if err := p.VerifyOrderPriceBeforePay(ctx, invoiceID(t, f.orderID), f.userID); err != nil {
			t.Fatalf("无快照应放行，实得 %v", err)
		}
	})
}

// 冻结续费价必须剔掉一次性初装费：否则成交额 33（周期费 28 + 初装费 5）会被冻结成续费价，
// 每次续费都多收 5 元。
func TestFrozenRenewValue(t *testing.T) {
	// 无利润加成：33 - 5 = 28
	if got := frozenRenewValue(33, 28, 5, 0, 0); got != 28 {
		t.Fatalf("应得 28，实得 %v", got)
	}
	// 百分比利润对「周期费+初装费」整体加成：36.3 - 5.5 = 30.8
	if got := frozenRenewValue(36.3, 28, 5, 0, 10); got != 30.8 {
		t.Fatalf("应得 30.8，实得 %v", got)
	}
	// 固定利润：35 - 5 = 30
	if got := frozenRenewValue(35, 28, 5, 1, 2); got != 30 {
		t.Fatalf("应得 30，实得 %v", got)
	}
	// 无初装费：成交额即周期费售价，原样保留
	if got := frozenRenewValue(28, 28, 0, 0, 0); got != 28 {
		t.Fatalf("应得 28，实得 %v", got)
	}
}

// 冻结价写回 services.renew_*：无初装费时保持成交额（老行为不变），有初装费时剔掉。
func TestFrozenRenewAmount(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	p := &Payment{db: d, Products: repo.NewProducts(d)}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	t.Run("剔除初装费份额", func(t *testing.T) {
		f := setupPayFixture(t, d, "33.00",
			sql.NullString{String: `{"quote":{"total":28,"setup":5},"selection":{}}`, Valid: true}, 0)
		if got := p.frozenRenewAmount(ctx, tx, f.orderID, "33.00"); got != "28.00" {
			t.Fatalf("应冻结 28.00，实得 %s", got)
		}
	})
	t.Run("无初装费的订单保持成交额", func(t *testing.T) {
		f := setupPayFixture(t, d, "27.50",
			sql.NullString{String: `{"quote":{"total":27.5},"selection":{}}`, Valid: true}, 0)
		if got := p.frozenRenewAmount(ctx, tx, f.orderID, "27.50"); got != "27.50" {
			t.Fatalf("应保持 27.50，实得 %s", got)
		}
	})
	t.Run("无快照的老订单保持成交额", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", sql.NullString{}, 0)
		if got := p.frozenRenewAmount(ctx, tx, f.orderID, "11.00"); got != "11.00" {
			t.Fatalf("应保持 11.00，实得 %s", got)
		}
	})
}

// orderCostAmount 是开通前比价的基准，取错会让比价整体失效（取大了什么都拦，取小了什么都放行）。
func TestOrderCostAmount(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	p := &Payment{db: d}

	t.Run("取 config_snapshot.quote.total", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00",
			sql.NullString{String: `{"quote":{"base":10,"total":12.5},"selection":{"mem":"2"}}`, Valid: true}, 0)
		if got := p.orderCostAmount(ctx, f.serviceID); got != 12.5 {
			t.Fatalf("应取到 12.5，实得 %v", got)
		}
	})
	t.Run("无快照返回 0（老数据）", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", sql.NullString{}, 0)
		if got := p.orderCostAmount(ctx, f.serviceID); got != 0 {
			t.Fatalf("无快照应返回 0，实得 %v", got)
		}
	})
	t.Run("快照缺 quote 字段返回 0", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", sql.NullString{String: `{"selection":{}}`, Valid: true}, 0)
		if got := p.orderCostAmount(ctx, f.serviceID); got != 0 {
			t.Fatalf("缺 quote 应返回 0，实得 %v", got)
		}
	})
	t.Run("快照为 JSON null 返回 0", func(t *testing.T) {
		// config_snapshot 是 JSONB，存不进非法 JSON；这里覆盖"合法但无内容"的 null。
		f := setupPayFixture(t, d, "11.00", sql.NullString{String: `null`, Valid: true}, 0)
		if got := p.orderCostAmount(ctx, f.serviceID); got != 0 {
			t.Fatalf("JSON null 应返回 0，实得 %v", got)
		}
	})
	t.Run("服务不存在返回 0", func(t *testing.T) {
		if got := p.orderCostAmount(ctx, -1); got != 0 {
			t.Fatalf("服务不存在应返回 0，实得 %v", got)
		}
	})
}

// 开通前退款：全额退实付 + 订单作废 + 服务终止，且必须可重入（不能重复退款）。
func TestRefundPendingService(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	p := &Payment{db: d}

	t.Run("全额退款并关闭订单与服务", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", sql.NullString{}, 0)
		before := f.balance(t)
		if err := p.RefundPendingService(ctx, 1, f.serviceID, "上游涨价"); err != nil {
			t.Fatalf("退款失败: %v", err)
		}
		if got := f.balance(t); got != before+11 {
			t.Fatalf("余额应 %v → %v，实得 %v", before, before+11, got)
		}
		if s := f.orderStatus(t); s != 2 {
			t.Fatalf("订单应作废(2)，实得 %d", s)
		}
		if s := f.serviceStatus(t); s != 3 {
			t.Fatalf("服务应终止(3)，实得 %d", s)
		}
	})

	t.Run("重复点击不重复退款", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", sql.NullString{}, 0)
		if err := p.RefundPendingService(ctx, 1, f.serviceID, "上游涨价"); err != nil {
			t.Fatal(err)
		}
		after := f.balance(t)
		// 服务已终止 → 第二次应被状态校验挡下，且不得再动余额。
		if err := p.RefundPendingService(ctx, 1, f.serviceID, "上游涨价"); err == nil {
			t.Fatal("已终止的服务不应再次退款")
		}
		if got := f.balance(t); got != after {
			t.Fatalf("余额不应再变化：%v → %v", after, got)
		}
	})

	t.Run("退款成功但关单失败后重试只补齐关单", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", sql.NullString{}, 0)
		// 模拟"钱已退、关单没跑完"：直接写退款记录，订单与服务保持原状。
		if _, err := d.ExecContext(ctx,
			`INSERT INTO refunds(user_id,order_id,amount,method,reason,admin_id,status)
			 VALUES($1,$2,'11.00','balance','上次已退',1,'done')`, f.userID, f.orderID); err != nil {
			t.Fatal(err)
		}
		before := f.balance(t)
		if err := p.RefundPendingService(ctx, 1, f.serviceID, "上游涨价"); err != nil {
			t.Fatalf("重试应补齐关单而不是报错: %v", err)
		}
		if got := f.balance(t); got != before {
			t.Fatalf("已退过款不应再退，余额 %v → %v", before, got)
		}
		if s := f.orderStatus(t); s != 2 {
			t.Fatalf("订单应作废(2)，实得 %d", s)
		}
		if s := f.serviceStatus(t); s != 3 {
			t.Fatalf("服务应终止(3)，实得 %d", s)
		}
	})

	t.Run("非待开通状态拒绝", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", sql.NullString{}, 1) // 1 = 已激活
		before := f.balance(t)
		if err := p.RefundPendingService(ctx, 1, f.serviceID, "上游涨价"); err == nil {
			t.Fatal("非待开通服务不应退款")
		}
		if got := f.balance(t); got != before {
			t.Fatalf("余额不应变化：%v → %v", before, got)
		}
	})

	t.Run("订单未支付时拒绝退款", func(t *testing.T) {
		f := setupPayFixture(t, d, "11.00", sql.NullString{}, 0)
		if _, err := d.ExecContext(ctx, `UPDATE invoices SET status=0 WHERE order_id=$1`, f.orderID); err != nil {
			t.Fatal(err)
		}
		before := f.balance(t)
		if err := p.RefundPendingService(ctx, 1, f.serviceID, "上游涨价"); err == nil {
			t.Fatal("未支付订单不应退款")
		}
		if got := f.balance(t); got != before {
			t.Fatalf("余额不应变化：%v → %v", before, got)
		}
		if s := f.serviceStatus(t); s != 0 {
			t.Fatalf("失败后服务应保持待开通，实得 %d", s)
		}
	})
}
