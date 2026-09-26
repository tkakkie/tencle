package spike_test

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tkakkie/tencle/internal/platform/db"
)

// seed は、2つのテナントとそれぞれの所属者・ノートを、スーパーユーザーで直接作る（RLS を通さずに用意するため）。
type seed struct {
	tenantA, tenantB, userA, userA2, userB, memberA, memberA2, memberB uuid.UUID
}

func seedTwoTenants(t *testing.T, e *env) seed {
	t.Helper()
	ctx := context.Background()
	s := seed{tenantA: uuid.New(), tenantB: uuid.New(), userA: uuid.New(), userA2: uuid.New(), userB: uuid.New(), memberA: uuid.New(), memberA2: uuid.New(), memberB: uuid.New()}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants (id, slug, name) VALUES ($1, 'a', 'A'), ($2, 'b', 'B')`, []any{s.tenantA, s.tenantB}},
		{`INSERT INTO users (id, email, hashed_password) VALUES ($1, 'a@example.test', 'secret-a'), ($2, 'a2@example.test', 'secret-a2'), ($3, 'b@example.test', 'secret-b')`, []any{s.userA, s.userA2, s.userB}},
		{`INSERT INTO memberships (tenant_id, id, user_id, access_level) VALUES ($1, $2, $3, 'admin'), ($1, $4, $5, 'office'), ($6, $7, $8, 'admin')`, []any{s.tenantA, s.memberA, s.userA, s.memberA2, s.userA2, s.tenantB, s.memberB, s.userB}},
		{`INSERT INTO user_events (user_id, event) VALUES ($1, 'password.reset'), ($2, 'password.reset')`, []any{s.userA, s.userB}},
		{`INSERT INTO notes (tenant_id, body, created_by_membership_id) VALUES ($1, 'note-a', $2), ($3, 'note-b', $4)`, []any{s.tenantA, s.memberA, s.tenantB, s.memberB}},
		{`INSERT INTO auth_tokens (token_hash, kind, subject_id, user_id, expires_at) VALUES ('\x01', 'session', $1, $1, now() + interval '1 hour'), ('\x02', 'session', $2, $2, now() + interval '1 hour')`, []any{s.userA, s.userB}},
	} {
		if _, err := e.superuser.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	return s
}

func count(t *testing.T, ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, sql string) int {
	t.Helper()
	var n int
	if err := q.QueryRow(ctx, sql).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func TestAppRoleIsRestricted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var super, bypass bool
	if err := e.app.QueryRow(ctx, `SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&super, &bypass); err != nil {
		t.Fatal(err)
	}
	if super || bypass {
		t.Fatalf("アプリ用ロール: rolsuper=%v rolbypassrls=%v, want 両方 false", super, bypass)
	}
	if n := count(t, ctx, e.app, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r' AND pg_get_userbyid(c.relowner) = current_user`); n != 0 {
		t.Fatalf("アプリ用ロールが所有するテーブル: %d, want 0", n)
	}
	// RLS の対象の各テーブルで、RLS が有効かつ強制されている（I-2）。
	for _, table := range []string{"tenants", "users", "auth_tokens", "memberships", "temples", "invitations", "notes", "audit_logs", "user_events"} {
		var enabled, forced bool
		if err := e.superuser.QueryRow(ctx, `SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = $1::regclass`, table).Scan(&enabled, &forced); err != nil {
			t.Fatal(err)
		}
		if !enabled || !forced {
			t.Errorf("%s: RLS 有効=%v 強制=%v", table, enabled, forced)
		}
	}
}

func TestRLS(t *testing.T) {
	e := newEnv(t)
	s := seedTwoTenants(t, e)
	ctx := context.Background()

	t.Run("文脈なしでは何も見えない", func(t *testing.T) {
		for _, table := range []string{"tenants", "users", "auth_tokens", "memberships", "notes"} {
			if n := count(t, ctx, e.app, "SELECT count(*) FROM "+table); n != 0 {
				t.Errorf("%s: %d 行見えた", table, n)
			}
		}
	})

	t.Run("自テナントだけが見える", func(t *testing.T) {
		err := db.TenantTx(ctx, e.app, s.tenantA, s.userA, func(tx pgx.Tx) error {
			if n := count(t, ctx, tx, `SELECT count(*) FROM notes`); n != 1 {
				t.Errorf("notes: %d, want 1", n)
			}
			if n := count(t, ctx, tx, `SELECT count(*) FROM notes WHERE tenant_id = '`+s.tenantB.String()+`'`); n != 0 {
				t.Errorf("別テナントの notes: %d, want 0", n)
			}
			if n := count(t, ctx, tx, `SELECT count(*) FROM tenants`); n != 1 {
				t.Errorf("tenants: %d, want 1", n)
			}
			// users は自分と現テナントの所属者だけ（I-34）。同じテナントの別の User は見え、別テナントの User は見えない。
			if n := count(t, ctx, tx, `SELECT count(*) FROM users`); n != 2 {
				t.Errorf("users: %d, want 2", n)
			}
			if n := count(t, ctx, tx, `SELECT count(*) FROM users WHERE email = 'b@example.test'`); n != 0 {
				t.Errorf("別テナントの User: %d", n)
			}
			// user_events は本人のものだけ。
			if n := count(t, ctx, tx, `SELECT count(*) FROM user_events`); n != 1 {
				t.Errorf("user_events: %d, want 1", n)
			}
			// auth_tokens は自分のものだけ。
			if n := count(t, ctx, tx, `SELECT count(*) FROM auth_tokens`); n != 1 {
				t.Errorf("auth_tokens: %d, want 1", n)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("テナントなしの文脈では自分だけが見える", func(t *testing.T) {
		err := db.UserTx(ctx, e.app, s.userA, func(tx pgx.Tx) error {
			if n := count(t, ctx, tx, `SELECT count(*) FROM users`); n != 1 {
				t.Errorf("users: %d, want 1（自分だけ）", n)
			}
			if n := count(t, ctx, tx, `SELECT count(*) FROM memberships`); n != 0 {
				t.Errorf("memberships: %d, want 0", n)
			}
			// 自分の Membership の一覧（例外関数）は、自分の所属だけを返す。
			if n := count(t, ctx, tx, `SELECT count(*) FROM fn_my_memberships()`); n != 1 {
				t.Errorf("fn_my_memberships: %d, want 1", n)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("別テナントの行は書けない", func(t *testing.T) {
		err := db.TenantTx(ctx, e.app, s.tenantA, s.userA, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO notes (tenant_id, body, created_by_membership_id) VALUES ($1, 'x', $2)`, s.tenantB, s.memberB)
			return err
		})
		if !isRLSViolation(err) {
			t.Fatalf("err = %v, want RLS の違反", err)
		}
		// 更新は、見えない行を対象にしないので 0 行になる。
		err = db.TenantTx(ctx, e.app, s.tenantA, s.userA, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE notes SET body = 'x' WHERE tenant_id = $1`, s.tenantB)
			if tag.RowsAffected() != 0 {
				t.Errorf("別テナントの更新: %d 行", tag.RowsAffected())
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("文脈はトランザクションの外に漏れない", func(t *testing.T) {
		// 接続を1本にして、同じ接続が使い回されることを保証する。
		cfg := e.app.Config().Copy()
		cfg.MaxConns = 1
		one, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer one.Close()
		if err := db.TenantTx(ctx, one, s.tenantA, s.userA, func(pgx.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if n := count(t, ctx, one, `SELECT count(*) FROM notes`); n != 0 {
			t.Errorf("コミットの後: %d 行見えた", n)
		}
		if n := count(t, ctx, one, `SELECT count(*) FROM users`); n != 0 {
			t.Errorf("コミットの後: users が %d 行見えた（ユーザー文脈が残った）", n)
		}
		if n := count(t, ctx, one, `SELECT count(*) FROM user_events`); n != 0 {
			t.Errorf("コミットの後: user_events が %d 行見えた", n)
		}
		errRollback := errors.New("戻す")
		if err := db.TenantTx(ctx, one, s.tenantA, s.userA, func(pgx.Tx) error { return errRollback }); !errors.Is(err, errRollback) {
			t.Fatal(err)
		}
		for _, table := range []string{"notes", "users", "auth_tokens", "user_events"} {
			if n := count(t, ctx, one, "SELECT count(*) FROM "+table); n != 0 {
				t.Errorf("ロールバックの後: %s が %d 行見えた", table, n)
			}
		}
	})

	t.Run("秘密の列は読めない", func(t *testing.T) {
		for _, sql := range []string{`SELECT hashed_password FROM users`, `SELECT token_hash FROM auth_tokens`} {
			err := db.TenantTx(ctx, e.app, s.tenantA, s.userA, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, sql)
				return err
			})
			if !isPermissionDenied(err) {
				t.Errorf("%s: err = %v, want 権限なし", sql, err)
			}
		}
	})

	t.Run("アプリ用ロールはテナントを作れない", func(t *testing.T) {
		_, err := e.app.Exec(ctx, `INSERT INTO tenants (slug, name) VALUES ('c', 'C')`)
		if !isPermissionDenied(err) {
			t.Fatalf("err = %v, want 権限なし", err)
		}
	})
}

func TestExceptionFunctions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	rows, err := e.superuser.Query(ctx, `
		SELECT p.proname, pg_get_userbyid(p.proowner), p.prosecdef, coalesce(array_to_string(p.proconfig, ','), ''),
		       has_function_privilege('public', p.oid, 'EXECUTE'), has_function_privilege('tencle_app', p.oid, 'EXECUTE')
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = 'public' AND p.proname LIKE 'fn\_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, owner, config string
		var definer, public, app bool
		if err := rows.Scan(&name, &owner, &definer, &config, &public, &app); err != nil {
			t.Fatal(err)
		}
		seen++
		if owner != "tencle_fn_reader" && owner != "tencle_fn_writer" {
			t.Errorf("%s: 所有者 %s", name, owner)
		}
		if !definer {
			t.Errorf("%s: SECURITY DEFINER でない", name)
		}
		if config != "search_path=pg_catalog, public, pg_temp" {
			t.Errorf("%s: search_path %q", name, config)
		}
		if public {
			t.Errorf("%s: PUBLIC が実行できる", name)
		}
		if !app {
			t.Errorf("%s: アプリ用ロールが実行できない", name)
		}
	}
	if seen == 0 {
		t.Fatal("例外関数が見つからない")
	}
	// 所有ロールは、表にない列を読めない（例：読み取り用は hashed_password 以外の users の列の更新ができない）。
	var canUpdate bool
	if err := e.superuser.QueryRow(ctx, `SELECT has_column_privilege('tencle_fn_reader', 'users', 'hashed_password', 'UPDATE')`).Scan(&canUpdate); err != nil {
		t.Fatal(err)
	}
	if canUpdate {
		t.Error("読み取り用の所有ロールが hashed_password を更新できる")
	}
}

func isPermissionDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

func isRLSViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501" && pgErr.Message != ""
}
