package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/tkakkie/tencle/db"
)

// Bootstrap は、スーパーユーザーの接続でロールとデータベースの初期設定を行う。
func Bootstrap(ctx context.Context, superuserURL string) error {
	pool, err := pgxpool.New(ctx, superuserURL)
	if err != nil {
		return fmt.Errorf("接続: %w", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, db.Bootstrap); err != nil {
		return fmt.Errorf("初期設定: %w", err)
	}
	return nil
}

// Migrate は、マイグレーション用ロールの接続で River と tencle のマイグレーションを適用する。
// River のテーブルへの権限を tencle のマイグレーションで与えるので、River を先に適用する。
func Migrate(ctx context.Context, migratorURL string) error {
	pool, err := pgxpool.New(ctx, migratorURL)
	if err != nil {
		return fmt.Errorf("接続: %w", err)
	}
	defer pool.Close()

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("River のマイグレーションの準備: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("River のマイグレーション: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()
	return gooseUp(ctx, sqlDB)
}

func gooseUp(ctx context.Context, sqlDB *sql.DB) error {
	migrations, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations)
	if err != nil {
		return fmt.Errorf("goose の準備: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose のマイグレーション: %w", err)
	}
	return nil
}
