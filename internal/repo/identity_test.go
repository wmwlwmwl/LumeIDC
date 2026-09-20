package repo

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// AdminSetPhone 写入失败时必须把错误交回调用方。
// 旧实现在 UPDATE 失败分支返回的是早已为 nil 的 err，调用方收到 (false, nil) 会当作
// "无需变更"继续按成功回执——管理员看到保存成功，手机号其实没改。
// 这里用真实可达的场景触发：仅手机号注册（email 为 NULL）的用户被清空手机号，
// 会违反 users_login_identifier_check（邮箱与手机号至少要有一个）。
func TestAdminSetPhoneReportsWriteFailure(t *testing.T) {
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

	// 每次运行换号：手机号唯一索引会让固定号码在残留数据上直接插入失败，
	// 那样报出的是夹具冲突，反而盖住要验证的错误传递。
	phone := fmt.Sprintf("+86139%08d", time.Now().UnixNano()%100000000)
	var userID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users(email,password_hash,phone_e164) VALUES(NULL,'测试',$1) RETURNING id`, phone).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	// 用 defer 而非 t.Cleanup：cleanup 在 defer d.Close() 之后执行，届时常量池已关闭，
	// 删除会静默失败并在共享测试库里留下残留行。defer 后注册者先跑，连接仍可用。
	defer func() {
		if _, err := d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, userID); err != nil {
			t.Errorf("清理测试用户失败: %v", err)
		}
	}()

	changed, err := NewIdentityStore(d).AdminSetPhone(ctx, userID, "", time.Now())
	if err == nil {
		t.Fatalf("清空唯一登录标识应因约束失败报错，实得 changed=%v err=nil", changed)
	}
	var stored sql.NullString
	if err := d.QueryRowContext(ctx, `SELECT phone_e164 FROM users WHERE id=$1`, userID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Valid || stored.String != phone {
		t.Fatalf("写入失败后手机号不得被改动，实得 %+v", stored)
	}
}

// 跨通道证件防重：同一证件号在人工/自动任一通道存在生效中记录时，
// 另一通道的其他账号提交必须被拒（一证一绑）。
func TestIdentityNumberCrossChannelUnique(t *testing.T) {
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
	store := NewIdentityStore(d)

	// 每次运行换标识，避免唯一索引与残留数据冲突。
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1000000000)
	hmacA := "test-xchan-hmac-a-" + suffix
	mkUser := func() int64 {
		var id int64
		if err := d.QueryRowContext(ctx,
			`INSERT INTO users(email,password_hash,phone_e164) VALUES(NULL,'测试',$1) RETURNING id`,
			"+86155"+fmt.Sprintf("%08d", time.Now().UnixNano()%100000000)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	u1, u2 := mkUser(), mkUser()
	defer func() {
		// ON DELETE CASCADE 会清掉两通道的提交记录。
		if _, err := d.ExecContext(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, u1, u2); err != nil {
			t.Errorf("清理测试用户失败: %v", err)
		}
	}()

	// 用户1：人工通道已有 approved 记录（证件 A）。
	if _, err := d.ExecContext(ctx,
		`INSERT INTO manual_identity_submissions(user_id,status,legal_name_ciphertext,identity_number_ciphertext,identity_number_hmac,front_photo_ref,back_photo_ref) VALUES($1,'approved','n','c',$2,'f','b')`,
		u1, hmacA); err != nil {
		t.Fatal(err)
	}

	// 用户2：自动通道提交同证件 A → 必须被拒。
	_, err = store.CreatePluginSubmission(ctx, u2, "stay33", "ref-a-"+suffix, "", "n", "c", hmacA, time.Now())
	if err == nil || err.Error() != "该证件已被其他账号使用" {
		t.Fatalf("跨通道同证件应被拒，实得: %v", err)
	}

	// 用户2：换证件 B → 放行（此后证件 B 处于 pending）。
	if _, err = store.CreatePluginSubmission(ctx, u2, "stay33", "ref-b-"+suffix, "", "n", "c", "test-xchan-hmac-b-"+suffix, time.Now()); err != nil {
		t.Fatalf("不同证件应放行: %v", err)
	}

	// 反向：证件 B 在自动通道生效中，其他账号走人工通道提交同证件 → 必须被拒。
	// 注意须用无人工记录的第三个用户：本人已有 approved 时会被前置状态检查先拦截，覆盖不到防重分支。
	u3 := mkUser()
	defer func() {
		if _, err := d.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, u3); err != nil {
			t.Errorf("清理测试用户失败: %v", err)
		}
	}()
	_, err = store.CreateSubmission(ctx, u3, "n", "c", "test-xchan-hmac-b-"+suffix, "f", "b", time.Now(), false)
	if err == nil || err.Error() != "该证件已被其他账号使用" {
		t.Fatalf("人工通道提交他人生效中的证件应被拒，实得: %v", err)
	}
}
