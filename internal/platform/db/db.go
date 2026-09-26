// Package db は PostgreSQL への接続と、テナント文脈を設定するトランザクションを提供する。
package db

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoTenant は、テナント文脈なしでテナント所有データに触ろうとしたときのエラー。
var ErrNoTenant = errors.New("db: テナントが指定されていません")

// TenantTx は、テナント文脈（app.tenant_id）とユーザー文脈（app.user_id）を設定したトランザクションの中で fn を実行する（I-1・I-2）。
func TenantTx(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, fn func(pgx.Tx) error) error {
	if tenantID == uuid.Nil() {
		return ErrNoTenant
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := SetContext(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		return fn(tx)
	})
}

// UserTx は、テナントを確定する前（ログイン済み・テナントなしのルート）に、ユーザー文脈だけを設定して fn を実行する。
func UserTx(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := SetContext(ctx, tx, uuid.Nil(), userID); err != nil {
			return err
		}
		return fn(tx)
	})
}

// SetContext は、実行中のトランザクションの文脈を設定し直す。招待の受諾のように、例外関数（テナントの確定前）で
// 始めた処理を同じトランザクションのままテナント文脈に切り替えるときに使う。接続はプールで使い回されるので、
// トランザクションの外に漏れない set_config(..., true)（SET LOCAL）でだけ設定する。
func SetContext(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true)`,
		text(tenantID), text(userID)); err != nil {
		return fmt.Errorf("文脈の設定: %w", err)
	}
	return nil
}

func text(id uuid.UUID) string {
	if id == uuid.Nil() {
		return ""
	}
	return id.String()
}
