// Package tenancy は、テナントの作成（テナント作成用ロールの CLI から）を行う。
package tenancy

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/tkakkie/tencle/internal/identity"
	"github.com/tkakkie/tencle/internal/mail"
	"github.com/tkakkie/tencle/internal/platform/db"
)

// ErrSlugTaken は、同じ slug のテナントが既にあるときのエラー。再実行しても何も変わらない。
var ErrSlugTaken = errors.New("tenancy: その slug は使われています")

// Create は、Tenant・自寺・最初の admin の User と Membership・監査を1つのトランザクションで作り、
// パスワード設定のメールのジョブを同じトランザクションで入れる（I-31）。途中で失敗したら何も残らない。
// pool はテナント作成用ロールの接続。
func Create(ctx context.Context, pool *pgxpool.Pool, jobs *river.Client[pgx.Tx], slug, name, adminEmail string) (tenantID uuid.UUID, err error) {
	tenantID = uuid.New()
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, slug, name) VALUES ($1, $2, $3)`, tenantID, slug, name); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrSlugTaken
			}
			return fmt.Errorf("Tenant の作成: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO temples (tenant_id, kind, name) VALUES ($1, 'own', $2)`, tenantID, name); err != nil {
			return fmt.Errorf("自寺の作成: %w", err)
		}
		// 既に User があれば再利用する（別テナントにも所属している人）。
		var userID uuid.UUID
		newUser := false
		err := tx.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, adminEmail).Scan(&userID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			userID, newUser = uuid.New(), true
			// パスワードは、メールのトークンで本人が設定するまで使えない値にしておく。
			if _, err := tx.Exec(ctx, `INSERT INTO users (id, email, hashed_password) VALUES ($1, $2, '!')`, userID, adminEmail); err != nil {
				return fmt.Errorf("User の作成: %w", err)
			}
		case err != nil:
			return fmt.Errorf("User の照合: %w", err)
		}
		membershipID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, id, user_id, access_level) VALUES ($1, $2, $3, 'admin')`, tenantID, membershipID, userID); err != nil {
			return fmt.Errorf("Membership の作成: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (tenant_id, actor_kind, actor_system, action, target_id) VALUES ($1, 'system', 'create-tenant', 'tenant.created', $1)`, tenantID); err != nil {
			return fmt.Errorf("監査の記録: %w", err)
		}
		if newUser {
			if _, err := jobs.InsertTx(ctx, tx, mail.PasswordSetupArgs{UserID: userID}, nil); err != nil {
				return fmt.Errorf("メールのジョブ: %w", err)
			}
		}
		return nil
	})
	return tenantID, err
}

// Invite は、admin が職員を招待する。招待・監査・メールのジョブを同じトランザクションで確定する（I-44）。
// pool はアプリ用ロールの接続。
func Invite(ctx context.Context, pool *pgxpool.Pool, jobs *river.Client[pgx.Tx], actor identity.Membership, userID uuid.UUID, email string, level identity.AccessLevel) (uuid.UUID, error) {
	if actor.Level != identity.LevelAdmin {
		return uuid.Nil(), identity.ErrForbidden
	}
	invitationID := uuid.New()
	err := db.TenantTx(ctx, pool, actor.TenantID, userID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO invitations (tenant_id, id, email, access_level, expires_at) VALUES ($1, $2, $3, $4, now() + interval '7 days')`,
			actor.TenantID, invitationID, email, level); err != nil {
			return fmt.Errorf("招待の作成: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (tenant_id, actor_kind, actor_membership_id, action, target_id) VALUES ($1, 'membership', $2, 'invitation.created', $3)`,
			actor.TenantID, actor.MembershipID, invitationID); err != nil {
			return fmt.Errorf("監査の記録: %w", err)
		}
		if _, err := jobs.InsertTx(ctx, tx, mail.InvitationArgs{InvitationID: invitationID}, nil); err != nil {
			return fmt.Errorf("メールのジョブ: %w", err)
		}
		return nil
	})
	return invitationID, err
}
