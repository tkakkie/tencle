package spike_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tkakkie/tencle/internal/platform/db"
)

// env は、テストごとに作る使い捨ての DB への、ロールごとの接続。
type env struct {
	app, creator, migrator, superuser *pgxpool.Pool
}

// newEnv は、スーパーユーザーで新しい DB を作り、初期設定とマイグレーションを適用する。
// テストはアプリ用ロールの接続（env.app）で行う（I-2）。スーパーユーザーの接続は、検証のための読み取りにだけ使う。
func newEnv(t *testing.T) *env {
	t.Helper()
	base := os.Getenv("TENCLE_TEST_SUPERUSER_URL")
	if base == "" {
		t.Skip("TENCLE_TEST_SUPERUSER_URL がないので DB のテストを飛ばす")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := fmt.Sprintf("spike_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}

	u := func(user, password string) string {
		parsed, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		parsed.Path = "/" + name
		if user != "" {
			parsed.User = url.UserPassword(user, password)
		}
		return parsed.String()
	}
	if err := db.Bootstrap(ctx, u("", "")); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, u("tencle_migrator", "dev-migrator")); err != nil {
		t.Fatal(err)
	}
	pool := func(user, password string) *pgxpool.Pool {
		p, err := pgxpool.New(ctx, u(user, password))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	return &env{
		app:       pool("tencle_app", "dev-app"),
		creator:   pool("tencle_tenant_creator", "dev-tenant-creator"),
		migrator:  pool("tencle_migrator", "dev-migrator"),
		superuser: pool("", ""),
	}
}
