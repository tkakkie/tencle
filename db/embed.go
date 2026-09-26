// Package db はマイグレーションと初期設定の SQL をバイナリに埋め込む。
package db

import (
	"embed"
	_ "embed"
)

// Migrations は goose のマイグレーション。
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Bootstrap は、スーパーユーザーで1回だけ実行する、ロールとデータベースの初期設定。
//
//go:embed bootstrap/bootstrap.sql
var Bootstrap string
