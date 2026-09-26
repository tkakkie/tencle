// Package identity は、スパイクの認証（ログイン・セッション・招待の受諾・パスワード再設定）と Membership の業務操作。
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tkakkie/tencle/internal/platform/db"
)

var (
	ErrInvalidCredentials = errors.New("identity: メールアドレスかパスワードが違います")
	ErrInvalidToken       = errors.New("identity: トークンが無効です")
	ErrLoginRequired      = errors.New("identity: 既にアカウントがあるので、ログインしてから受諾してください")
	ErrNotFound           = errors.New("identity: 見つかりません")
	ErrForbidden          = errors.New("identity: 権限がありません")
	ErrLastAdmin          = errors.New("identity: 最後の admin は無効化・降格できません")
)

// AccessLevel は Membership の権限の段階（§5.2）。
type AccessLevel string

const (
	LevelAdmin  AccessLevel = "admin"
	LevelOffice AccessLevel = "office"
	LevelPriest AccessLevel = "priest"
)

// Service は、アプリ用ロールの接続で動く。
type Service struct {
	Pool       *pgxpool.Pool
	SessionTTL time.Duration
	// AuditHook は、スパイクで監査の書き込みの失敗を起こすためだけのもの。
	AuditHook func() error
	// LoginHook は、スパイクでログインの照合と保存の間に割り込むためだけのもの。
	LoginHook func()
}

// NormalizeEmail は、メールアドレスを保存・比較する形にする。入力の境界（ログイン・招待・テナントの作成）で必ず通す。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Login は、パスワードを確かめてセッションを発行する。存在しないメールアドレスでも同じだけ時間をかける（I-34）。
func (s *Service) Login(ctx context.Context, email, password string) (token string, userID uuid.UUID, err error) {
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var hashed string
		var version int32
		err := tx.QueryRow(ctx, `SELECT user_id, hashed_password, credential_version FROM fn_login_lookup($1) WHERE user_id IS NOT NULL`, NormalizeEmail(email)).Scan(&userID, &hashed, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			verifyPassword(dummyHash, password)
			return ErrInvalidCredentials
		}
		if err != nil {
			return fmt.Errorf("ログインの照合: %w", err)
		}
		if !verifyPassword(hashed, password) {
			return ErrInvalidCredentials
		}
		if s.LoginHook != nil {
			s.LoginHook()
		}
		// 検証に使ったパスワードの世代をセッションに記録する。検証の後でパスワードが更新されていれば、このセッションは無効になる。
		token, err = s.issue(ctx, tx, TokenSession, userID, userID, time.Now().Add(s.SessionTTL), &version)
		return err
	})
	return token, userID, err
}

// Authenticate は、セッションのトークンから User を決める。毎回のリクエストで DB を確かめるので、失効は次のリクエストから効く（I-8）。
func (s *Service) Authenticate(ctx context.Context, token string) (uuid.UUID, error) {
	var userID *uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT user_id FROM fn_auth_token_check($1, $2)`, hashToken(token), TokenSession).Scan(&userID)
	if err != nil {
		return uuid.Nil(), fmt.Errorf("セッションの照合: %w", err)
	}
	if userID == nil {
		return uuid.Nil(), ErrInvalidToken
	}
	return *userID, nil
}

// Logout は、その User のすべてのセッションを失効させる。
func (s *Service) Logout(ctx context.Context, userID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `SELECT fn_revoke_user_sessions($1)`, userID)
	return err
}

// IssueToken は、メール送信ジョブがトークンを発行するときに使う（I-10）。セッションはログインでだけ発行する。
func (s *Service) IssueToken(ctx context.Context, tx pgx.Tx, kind TokenKind, subjectID, userID uuid.UUID, expires time.Time) (string, error) {
	if kind == TokenSession {
		return "", errors.New("identity: セッションはログインでだけ発行する")
	}
	return s.issue(ctx, tx, kind, subjectID, userID, expires, nil)
}

func (s *Service) issue(ctx context.Context, tx pgx.Tx, kind TokenKind, subjectID, userID uuid.UUID, expires time.Time, version *int32) (string, error) {
	token, hash := newToken()
	var user *uuid.UUID
	if userID != uuid.Nil() {
		user = &userID
	}
	if _, err := tx.Exec(ctx, `SELECT fn_auth_token_save($1, $2, $3, $4, $5, $6)`, hash, kind, subjectID, user, expires, version); err != nil {
		return "", fmt.Errorf("トークンの保存: %w", err)
	}
	return token, nil
}

// AcceptInvitation は、招待の受諾を1つのトランザクションで行う（不変条件の例外関数の節）。
// 既に User を持つ人は、その User でログインしている場合だけ受諾できる（loggedIn）。
func (s *Service) AcceptInvitation(ctx context.Context, token, password string, loggedIn uuid.UUID) (uuid.UUID, error) {
	var userID uuid.UUID
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var invitationID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT fn_auth_token_consume($1, $2)`, hashToken(token), TokenInvitation).Scan(&invitationID); err != nil {
			return fmt.Errorf("トークンの消費: %w", err)
		}
		if invitationID == nil {
			return ErrInvalidToken
		}
		var tenantID uuid.UUID
		var status, email string
		var level AccessLevel
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT tenant_id, status, expires_at, email, access_level FROM fn_invitation_lookup($1) WHERE tenant_id IS NOT NULL`, *invitationID).
			Scan(&tenantID, &status, &expires, &email, &level)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return fmt.Errorf("招待の照合: %w", err)
		}
		if status != "pending" || !expires.After(time.Now()) {
			return ErrInvalidToken
		}

		var existing *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT fn_user_by_email($1)`, email).Scan(&existing); err != nil {
			return fmt.Errorf("User の照合: %w", err)
		}
		switch {
		case existing != nil && *existing != loggedIn:
			return ErrLoginRequired
		case existing != nil:
			userID = *existing
		default:
			if err := tx.QueryRow(ctx, `SELECT fn_create_user_by_invitation($1, $2)`, email, hashPassword(password)).Scan(&userID); err != nil {
				return fmt.Errorf("User の作成: %w", err)
			}
		}

		// ここからは同じトランザクションのまま、テナント文脈でアプリ用ロールの権限で書く。
		if err := db.SetContext(ctx, tx, tenantID, userID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE invitations SET status = 'accepted' WHERE id = $1 AND status = 'pending'`, *invitationID)
		if err != nil {
			return fmt.Errorf("招待の消費: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return ErrInvalidToken
		}
		membershipID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, id, user_id, access_level) VALUES ($1, $2, $3, $4)`, tenantID, membershipID, userID, level); err != nil {
			return fmt.Errorf("Membership の作成: %w", err)
		}
		// 受諾の Actor は、作ったばかりの Membership 自身（System Actor をハンドラから作らないため。I-37）。
		return s.audit(ctx, tx, tenantID, membershipID, "membership.created", membershipID)
	})
	return userID, err
}

// ResetPassword は、トークンの消費とパスワードの更新（全セッションの失効を含む）を1つのトランザクションで行う。
func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var userID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT fn_auth_token_consume($1, $2)`, hashToken(token), TokenPasswordReset).Scan(&userID); err != nil {
			return fmt.Errorf("トークンの消費: %w", err)
		}
		if userID == nil {
			return ErrInvalidToken
		}
		if _, err := tx.Exec(ctx, `SELECT fn_update_password($1, $2)`, *userID, hashPassword(password)); err != nil {
			return fmt.Errorf("パスワードの更新: %w", err)
		}
		// テナントに属さない出来事は user_events に残す（ユーザー単位の監査先）。
		if err := db.SetContext(ctx, tx, uuid.Nil(), *userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_events (user_id, event) VALUES ($1, 'password.reset')`, *userID)
		return err
	})
}

// Membership は、ログインユーザーがテナントで持つ所属。
type Membership struct {
	TenantID     uuid.UUID
	MembershipID uuid.UUID
	Level        AccessLevel
}

// ResolveTenant は、URL の slug からテナントを決め、有効な Membership を確かめる。
// 存在しない slug と、Membership のない slug を同じ ErrNotFound にする（I-5）。
func (s *Service) ResolveTenant(ctx context.Context, userID uuid.UUID, slug string) (Membership, error) {
	var tenantID *uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT fn_tenant_by_slug($1)`, slug).Scan(&tenantID); err != nil {
		return Membership{}, fmt.Errorf("slug の解決: %w", err)
	}
	if tenantID == nil {
		return Membership{}, ErrNotFound
	}
	m := Membership{TenantID: *tenantID}
	err := db.TenantTx(ctx, s.Pool, *tenantID, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, access_level FROM memberships WHERE user_id = $1 AND active`, userID).Scan(&m.MembershipID, &m.Level)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	return m, err
}

// ChangeAccessLevel と Deactivate は業務操作（I-44）。集約の根の Tenant を先にロックしてから、
// 有効な admin の数を読み直して判断する。これで、2人の admin が同時に互いを降格しても1人は残る（I-31）。
func (s *Service) ChangeAccessLevel(ctx context.Context, actor Membership, userID, target uuid.UUID, level AccessLevel) error {
	return s.membershipOp(ctx, actor, userID, target, "membership.level_changed", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE memberships SET access_level = $2 WHERE id = $1`, target, level)
		return err
	}, level != LevelAdmin)
}

func (s *Service) Deactivate(ctx context.Context, actor Membership, userID, target uuid.UUID) error {
	return s.membershipOp(ctx, actor, userID, target, "membership.deactivated", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE memberships SET active = false WHERE id = $1`, target)
		return err
	}, true)
}

func (s *Service) membershipOp(ctx context.Context, actor Membership, userID, target uuid.UUID, action string, change func(pgx.Tx) error, removesAdmin bool) error {
	return db.TenantTx(ctx, s.Pool, actor.TenantID, userID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM tenants WHERE id = $1 FOR UPDATE`, actor.TenantID); err != nil {
			return fmt.Errorf("Tenant のロック: %w", err)
		}
		// 判断に使う行は、ロックを取った後に読み直す。
		var actorLevel AccessLevel
		var actorActive bool
		if err := tx.QueryRow(ctx, `SELECT access_level, active FROM memberships WHERE id = $1`, actor.MembershipID).Scan(&actorLevel, &actorActive); err != nil {
			return fmt.Errorf("操作者の確認: %w", err)
		}
		if !actorActive || actorLevel != LevelAdmin {
			return ErrForbidden
		}
		var targetLevel AccessLevel
		var targetActive bool
		if err := tx.QueryRow(ctx, `SELECT access_level, active FROM memberships WHERE id = $1`, target).Scan(&targetLevel, &targetActive); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("対象の確認: %w", err)
		}
		if removesAdmin && targetLevel == LevelAdmin && targetActive {
			var admins int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE access_level = 'admin' AND active`).Scan(&admins); err != nil {
				return err
			}
			if admins <= 1 {
				return ErrLastAdmin
			}
		}
		if err := change(tx); err != nil {
			return fmt.Errorf("Membership の変更: %w", err)
		}
		return s.audit(ctx, tx, actor.TenantID, actor.MembershipID, action, target)
	})
}

func (s *Service) audit(ctx context.Context, tx pgx.Tx, tenantID, actorMembershipID uuid.UUID, action string, target uuid.UUID) error {
	if s.AuditHook != nil {
		if err := s.AuditHook(); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs (tenant_id, actor_kind, actor_membership_id, action, target_id) VALUES ($1, 'membership', $2, $3, $4)`,
		tenantID, actorMembershipID, action, target)
	if err != nil {
		return fmt.Errorf("監査の記録: %w", err)
	}
	return nil
}
