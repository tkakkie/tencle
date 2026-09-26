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
	"github.com/riverqueue/river"

	"github.com/tkakkie/tencle/internal/identity"
)

// Sender はメールの送信先。本番の送信サービスと、テストの偽物がある。
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// errSend は、送信の失敗を River に返すときの値。送信サービスのエラーにはメールアドレスなどが入り得るので、
// そのまま返さない（River はエラーの文字列をジョブの行に保存する。I-19・I-20）。
var errSend = errors.New("mail: 送信に失敗しました")

type PasswordSetupArgs struct {
	UserID uuid.UUID `json:"user_id"`
}

func (PasswordSetupArgs) Kind() string { return "send_password_setup" }

type InvitationArgs struct {
	InvitationID uuid.UUID `json:"invitation_id"`
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
		token, err = w.Identity.IssueToken(ctx, tx, identity.TokenPasswordReset, job.Args.UserID, job.Args.UserID, 72*time.Hour)
		return err
	})
	if err != nil {
		return err
	}
	return send(ctx, w.Sender, job.Kind, to, "パスワードの設定", "/password/reset?token="+token)
}

type InvitationWorker struct {
	river.WorkerDefaults[InvitationArgs]
	Identity *identity.Service
	Sender   Sender
}

func (w *InvitationWorker) Work(ctx context.Context, job *river.Job[InvitationArgs]) error {
	var to, token string
	err := pgx.BeginFunc(ctx, w.Identity.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT email FROM fn_invitation_lookup($1)`, job.Args.InvitationID).Scan(&to); err != nil {
			return fmt.Errorf("宛先の取得: %w", err)
		}
		var err error
		token, err = w.Identity.IssueToken(ctx, tx, identity.TokenInvitation, job.Args.InvitationID, uuid.Nil(), 72*time.Hour)
		return err
	})
	if err != nil {
		return err
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
