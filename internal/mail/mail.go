// Package mail は、認証のメールを送るジョブ（River）。トークンはジョブの中で発行し、ハッシュだけを保存する（I-10）。
// ジョブの引数には ID だけを入れ、メールアドレスなどの個人情報を入れない（I-20）。
package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"

	"github.com/tkakkie/tencle/internal/identity"
	"github.com/tkakkie/tencle/internal/platform/db"
)

// Sender はメールの送信先。本番の送信サービスと、テストの偽物がある。
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// ワーカーが River に返すエラーは、この固定の値だけにする。River はエラーの文字列をジョブの行に保存し、
// 送信サービスのエラーにも、pgx のエラー（サーバーのメッセージ、引数の変換の失敗）にも値が入り得るため（I-19・I-20、ADR 0007）。
var (
	errSend = errors.New("mail: 送信に失敗しました")
	errJob  = errors.New("mail: ジョブの処理に失敗しました")
)

// Sanitize は、ワーカーの境界でエラーを固定の値に変換する。DB のエラーは SQLSTATE だけを残す（値を含まない）。
func Sanitize(err error) error {
	if err == nil || errors.Is(err, errSend) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("%w（SQLSTATE %s）", errJob, pgErr.Code)
	}
	return errJob
}

type PasswordSetupArgs struct {
	UserID uuid.UUID `json:"user_id"`
}

func (PasswordSetupArgs) Kind() string { return "send_password_setup" }

// InvitationArgs は、テナント所有データを扱うジョブの引数（I-20）：テナント・依頼者の Actor・引数の版。
type InvitationArgs struct {
	Version               int       `json:"v"`
	TenantID              uuid.UUID `json:"tenant_id"`
	RequesterMembershipID uuid.UUID `json:"requester_membership_id"`
	InvitationID          uuid.UUID `json:"invitation_id"`
}

func (InvitationArgs) Kind() string { return "send_invitation" }

type PasswordSetupWorker struct {
	river.WorkerDefaults[PasswordSetupArgs]
	Identity *identity.Service
	Sender   Sender
}

func (w *PasswordSetupWorker) Work(ctx context.Context, job *river.Job[PasswordSetupArgs]) error {
	var to, token string
	err := pgx.BeginFunc(ctx, w.Identity.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT fn_user_email($1)`, job.Args.UserID).Scan(&to); err != nil {
			return fmt.Errorf("宛先の取得: %w", err)
		}
		var err error
		token, err = w.Identity.IssueToken(ctx, tx, identity.TokenPasswordReset, job.Args.UserID, job.Args.UserID, time.Now().Add(72*time.Hour))
		return err
	})
	if err != nil {
		return Sanitize(err)
	}
	return send(ctx, w.Sender, job.Kind, to, "パスワードの設定", "/password/reset?token="+token)
}

type InvitationWorker struct {
	river.WorkerDefaults[InvitationArgs]
	Identity *identity.Service
	Sender   Sender
}

// Work は、テナント文脈で依頼者と招待を確かめ直してから送る。依頼者が無効化・降格されたか、
// 招待が取り消された・期限切れなら送らない（I-20：処理の時点で依頼者の権限を確かめる）。
// 宛先は、テナント文脈でアプリ用ロールのまま招待から読むので、例外関数は要らない。
func (w *InvitationWorker) Work(ctx context.Context, job *river.Job[InvitationArgs]) error {
	a := job.Args
	var to, token string
	allowed := false
	err := db.TenantTx(ctx, w.Identity.Pool, a.TenantID, uuid.Nil(), func(tx pgx.Tx) error {
		var level string
		var active bool
		err := tx.QueryRow(ctx, `SELECT access_level, active FROM memberships WHERE id = $1`, a.RequesterMembershipID).Scan(&level, &active)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !active || level != string(identity.LevelAdmin) {
			return nil
		}
		var status string
		var expires time.Time
		err = tx.QueryRow(ctx, `SELECT email, status, expires_at FROM invitations WHERE id = $1`, a.InvitationID).Scan(&to, &status, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status != "pending" || !expires.After(time.Now()) {
			return nil
		}
		allowed = true
		// トークンの期限は招待の期限と同じにする（ADR 0004）。
		token, err = w.Identity.IssueToken(ctx, tx, identity.TokenInvitation, a.InvitationID, uuid.Nil(), expires)
		return err
	})
	if err != nil {
		return Sanitize(err)
	}
	if !allowed {
		slog.InfoContext(ctx, "招待のメールを送らない", "job_kind", job.Kind, "invitation_id", a.InvitationID.String())
		return nil
	}
	return send(ctx, w.Sender, job.Kind, to, "招待", "/invitations/accept?token="+token)
}

func send(ctx context.Context, s Sender, kind, to, subject, body string) error {
	if err := s.Send(ctx, to, subject, body); err != nil {
		// ログにも送信サービスのエラーの本文を出さない。
		slog.WarnContext(ctx, "メールの送信に失敗", "job_kind", kind)
		return errSend
	}
	return nil
}
