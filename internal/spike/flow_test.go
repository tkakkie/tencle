package spike_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/tkakkie/tencle/internal/identity"
	"github.com/tkakkie/tencle/internal/mail"
	"github.com/tkakkie/tencle/internal/tenancy"
)

// fakeSender は、送ったメールを宛先ごとに受け取れる偽物。fail を設定すると、宛先を含むエラーで失敗する。
type fakeSender struct {
	mu    sync.Mutex
	boxes map[string]chan string
	fail  atomic.Bool
}

func (f *fakeSender) box(to string) chan string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.boxes == nil {
		f.boxes = map[string]chan string{}
	}
	if f.boxes[to] == nil {
		f.boxes[to] = make(chan string, 10)
	}
	return f.boxes[to]
}

func (f *fakeSender) Send(_ context.Context, to, _, body string) error {
	if f.fail.Load() {
		return fmt.Errorf("SMTP 550: mailbox %s unavailable", to)
	}
	f.box(to) <- body
	return nil
}

// token は、宛先に届いた次のメールからトークンを取り出す。
func (f *fakeSender) token(t *testing.T, to string) string {
	t.Helper()
	select {
	case body := <-f.box(to):
		_, token, ok := strings.Cut(body, "token=")
		if !ok {
			t.Fatalf("トークンがない: %q", body)
		}
		return token
	case <-time.After(10 * time.Second):
		t.Fatalf("%s にメールが届かない", to)
		return ""
	}
}

type app struct {
	*env
	id      *identity.Service
	sender  *fakeSender
	jobs    *river.Client[pgx.Tx] // アプリ用ロールでジョブを動かす
	creatorJobs *river.Client[pgx.Tx] // テナント作成用ロールでジョブを入れるだけ
}

func newApp(t *testing.T) *app {
	t.Helper()
	return newAppWithLogger(t, nil)
}

func newAppWithLogger(t *testing.T, logger *slog.Logger) *app {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	a := &app{env: e, id: &identity.Service{Pool: e.app, SessionTTL: time.Hour}, sender: &fakeSender{}}
	workers := river.NewWorkers()
	river.AddWorker(workers, &mail.PasswordSetupWorker{Identity: a.id, Sender: a.sender})
	river.AddWorker(workers, &mail.InvitationWorker{Identity: a.id, Sender: a.sender})
	var err error
	a.jobs, err = river.NewClient(riverpgxv5.New(e.app), &river.Config{
		Queues:            map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 4}},
		Workers:           workers,
		FetchPollInterval: 50 * time.Millisecond,
		FetchCooldown:     10 * time.Millisecond,
		Logger:            logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.jobs.Stop(context.Background()) })
	a.creatorJobs, err = river.NewClient(riverpgxv5.New(e.creator), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// newTenant は、CLI と同じ手順でテナントを作り、届いたメールで admin のパスワードを設定してログインする。
func (a *app) newTenant(t *testing.T, slug, email string) (identity.Membership, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := tenancy.Create(ctx, a.env.creator, a.creatorJobs, slug, slug, email); err != nil {
		t.Fatal(err)
	}
	if err := a.id.ResetPassword(ctx, a.sender.token(t, identity.NormalizeEmail(email)), "pw-"+email); err != nil {
		t.Fatal(err)
	}
	_, userID, err := a.id.Login(ctx, email, "pw-"+email)
	if err != nil {
		t.Fatal(err)
	}
	m, err := a.id.ResolveTenant(ctx, userID, slug)
	if err != nil {
		t.Fatal(err)
	}
	return m, userID
}

func TestCreateTenant(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	admin, _ := a.newTenant(t, "a", "admin@example.test")
	if admin.Level != identity.LevelAdmin {
		t.Fatalf("最初の Membership: %s", admin.Level)
	}

	t.Run("同じ slug の再実行は何も変えない", func(t *testing.T) {
		_, err := tenancy.Create(ctx, a.env.creator, a.creatorJobs, "a", "a", "other@example.test")
		if !errors.Is(err, tenancy.ErrSlugTaken) {
			t.Fatalf("err = %v", err)
		}
		if n := count(t, ctx, a.superuser, `SELECT count(*) FROM users WHERE email = 'other@example.test'`); n != 0 {
			t.Errorf("User が残った: %d", n)
		}
	})

	t.Run("途中で失敗したら何も残らない", func(t *testing.T) {
		// 監査の記録で失敗させる（トリガーはスーパーユーザーで一時的に置く）。
		for _, sql := range []string{
			`CREATE FUNCTION fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$`,
			`CREATE TRIGGER fail_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION fail_audit()`,
		} {
			if _, err := a.superuser.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
		}
		_, err := tenancy.Create(ctx, a.env.creator, a.creatorJobs, "broken", "broken", "broken@example.test")
		if _, dropErr := a.superuser.Exec(ctx, `DROP TRIGGER fail_audit ON audit_logs`); dropErr != nil {
			t.Fatal(dropErr)
		}
		if err == nil {
			t.Fatal("失敗しなかった")
		}
		for _, sql := range []string{
			`SELECT count(*) FROM tenants WHERE slug = 'broken'`,
			`SELECT count(*) FROM users WHERE email = 'broken@example.test'`,
			`SELECT count(*) FROM river_job WHERE kind = 'send_password_setup' AND state <> 'completed'`,
		} {
			if n := count(t, ctx, a.superuser, sql); n != 0 {
				t.Errorf("%s: %d", sql, n)
			}
		}
	})

	t.Run("既存の User を別テナントの admin にできる", func(t *testing.T) {
		tenantB, err := tenancy.Create(ctx, a.env.creator, a.creatorJobs, "b", "b", "admin@example.test")
		if err != nil {
			t.Fatal(err)
		}
		if n := count(t, ctx, a.superuser, `SELECT count(*) FROM memberships WHERE tenant_id = '`+tenantB.String()+`' AND access_level = 'admin'`); n != 1 {
			t.Errorf("B の admin: %d", n)
		}
	})
}

func TestInvitation(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	admin, adminUser := a.newTenant(t, "a", "admin@example.test")

	t.Run("新規の User が受諾し、ログインできる", func(t *testing.T) {
		if _, err := tenancy.Invite(ctx, a.app, a.jobs, admin, adminUser, "office@example.test", identity.LevelOffice); err != nil {
			t.Fatal(err)
		}
		userID, err := a.id.AcceptInvitation(ctx, a.sender.token(t, "office@example.test"), "pw-office", uuid.Nil())
		if err != nil {
			t.Fatal(err)
		}
		if _, got, err := a.id.Login(ctx, "office@example.test", "pw-office"); err != nil || got != userID {
			t.Fatalf("ログイン: %v %v", got, err)
		}
		m, err := a.id.ResolveTenant(ctx, userID, "a")
		if err != nil || m.Level != identity.LevelOffice {
			t.Fatalf("所属: %+v %v", m, err)
		}
		// 受諾の監査の Actor は、作った Membership 自身。
		if n := count(t, ctx, a.superuser, `SELECT count(*) FROM audit_logs WHERE action = 'membership.created' AND actor_membership_id = '`+m.MembershipID.String()+`'`); n != 1 {
			t.Errorf("受諾の監査: %d", n)
		}
	})

	t.Run("同じトークンを並行に使っても1回だけ成立する", func(t *testing.T) {
		if _, err := tenancy.Invite(ctx, a.app, a.jobs, admin, adminUser, "race@example.test", identity.LevelOffice); err != nil {
			t.Fatal(err)
		}
		token := a.sender.token(t, "race@example.test")
		const n = 8
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for range n {
			wg.Go(func() {
				_, err := a.id.AcceptInvitation(ctx, token, "pw-race", uuid.Nil())
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		ok := 0
		for err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, identity.ErrInvalidToken):
			default:
				t.Errorf("想定外のエラー: %v", err)
			}
		}
		if ok != 1 {
			t.Fatalf("成立した回数: %d, want 1", ok)
		}
	})

	t.Run("既に User を持つ人は、その User でログインしているときだけ受諾できる", func(t *testing.T) {
		otherAdmin, otherUser := a.newTenant(t, "c", "c-admin@example.test")
		if _, err := tenancy.Invite(ctx, a.app, a.jobs, otherAdmin, otherUser, "office@example.test", identity.LevelOffice); err != nil {
			t.Fatal(err)
		}
		token := a.sender.token(t, "office@example.test")
		if _, err := a.id.AcceptInvitation(ctx, token, "", uuid.Nil()); !errors.Is(err, identity.ErrLoginRequired) {
			t.Fatalf("未ログイン: %v", err)
		}
		// 失敗した受諾ではトークンを消費しない（全体がロールバックされる）。
		_, officeUser, err := a.id.Login(ctx, "office@example.test", "pw-office")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.id.AcceptInvitation(ctx, token, "", officeUser); err != nil {
			t.Fatalf("ログイン済み: %v", err)
		}
		if _, err := a.id.ResolveTenant(ctx, officeUser, "c"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("期限切れ・用途違い・使用済みのトークンは拒否する", func(t *testing.T) {
		if _, err := tenancy.Invite(ctx, a.app, a.jobs, admin, adminUser, "late@example.test", identity.LevelOffice); err != nil {
			t.Fatal(err)
		}
		token := a.sender.token(t, "late@example.test")
		if err := a.id.ResetPassword(ctx, token, "x"); !errors.Is(err, identity.ErrInvalidToken) {
			t.Errorf("用途違い: %v", err)
		}
		if _, err := a.superuser.Exec(ctx, `UPDATE auth_tokens SET expires_at = now() - interval '1 second' WHERE kind = 'invitation' AND consumed_at IS NULL`); err != nil {
			t.Fatal(err)
		}
		if _, err := a.id.AcceptInvitation(ctx, token, "x", uuid.Nil()); !errors.Is(err, identity.ErrInvalidToken) {
			t.Errorf("期限切れ: %v", err)
		}
	})
}

func TestSessions(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	admin, adminUser := a.newTenant(t, "a", "admin@example.test")
	if _, err := tenancy.Invite(ctx, a.app, a.jobs, admin, adminUser, "office@example.test", identity.LevelOffice); err != nil {
		t.Fatal(err)
	}
	officeUser, err := a.id.AcceptInvitation(ctx, a.sender.token(t, "office@example.test"), "pw-office", uuid.Nil())
	if err != nil {
		t.Fatal(err)
	}
	office, err := a.id.ResolveTenant(ctx, officeUser, "a")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("権限の変更と無効化は次のリクエストから効く", func(t *testing.T) {
		session, _, err := a.id.Login(ctx, "office@example.test", "pw-office")
		if err != nil {
			t.Fatal(err)
		}
		if err := a.id.ChangeAccessLevel(ctx, admin, adminUser, office.MembershipID, identity.LevelPriest); err != nil {
			t.Fatal(err)
		}
		// 同じセッションのまま、次のリクエストで権限を読み直す。
		u, err := a.id.Authenticate(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		if m, err := a.id.ResolveTenant(ctx, u, "a"); err != nil || m.Level != identity.LevelPriest {
			t.Fatalf("降格の後: %+v %v", m, err)
		}
		if err := a.id.Deactivate(ctx, admin, adminUser, office.MembershipID); err != nil {
			t.Fatal(err)
		}
		if _, err := a.id.ResolveTenant(ctx, u, "a"); !errors.Is(err, identity.ErrNotFound) {
			t.Fatalf("無効化の後: %v", err)
		}
	})

	t.Run("パスワードの再設定ですべてのセッションが失効する", func(t *testing.T) {
		s1, _, err := a.id.Login(ctx, "admin@example.test", "pw-admin@example.test")
		if err != nil {
			t.Fatal(err)
		}
		s2, _, err := a.id.Login(ctx, "admin@example.test", "pw-admin@example.test")
		if err != nil {
			t.Fatal(err)
		}
		// パスワード再設定のトークンを発行する（本番ではメールのジョブが行う）。
		var token string
		err = pgx.BeginFunc(ctx, a.app, func(tx pgx.Tx) error {
			var err error
			token, err = issueReset(ctx, a, tx, adminUser)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.id.ResetPassword(ctx, token, "new-pw"); err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{s1, s2} {
			if _, err := a.id.Authenticate(ctx, s); !errors.Is(err, identity.ErrInvalidToken) {
				t.Errorf("再設定の前のセッション: %v", err)
			}
		}
		if err := a.id.ResetPassword(ctx, token, "again"); !errors.Is(err, identity.ErrInvalidToken) {
			t.Errorf("使用済みのトークン: %v", err)
		}
	})

	t.Run("ログアウトしたセッションは使えない", func(t *testing.T) {
		s, u, err := a.id.Login(ctx, "admin@example.test", "new-pw")
		if err != nil {
			t.Fatal(err)
		}
		if err := a.id.Logout(ctx, u); err != nil {
			t.Fatal(err)
		}
		if _, err := a.id.Authenticate(ctx, s); !errors.Is(err, identity.ErrInvalidToken) {
			t.Fatalf("ログアウトの後: %v", err)
		}
	})

	t.Run("存在しないメールアドレスと違うパスワードは同じエラー", func(t *testing.T) {
		_, _, e1 := a.id.Login(ctx, "nobody@example.test", "x")
		_, _, e2 := a.id.Login(ctx, "admin@example.test", "x")
		if !errors.Is(e1, identity.ErrInvalidCredentials) || !errors.Is(e2, identity.ErrInvalidCredentials) {
			t.Fatalf("%v / %v", e1, e2)
		}
	})
}

func TestTenantBoundary(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	_, userA := a.newTenant(t, "a", "a@example.test")
	a.newTenant(t, "b", "b@example.test")
	_, errOther := a.id.ResolveTenant(ctx, userA, "b")
	_, errMissing := a.id.ResolveTenant(ctx, userA, "nothing")
	if !errors.Is(errOther, identity.ErrNotFound) || !errors.Is(errMissing, identity.ErrNotFound) {
		t.Fatalf("別テナント: %v / 存在しない: %v", errOther, errMissing)
	}
}

func TestLastAdmin(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	admin, adminUser := a.newTenant(t, "a", "a1@example.test")
	if _, err := tenancy.Invite(ctx, a.app, a.jobs, admin, adminUser, "a2@example.test", identity.LevelAdmin); err != nil {
		t.Fatal(err)
	}
	user2, err := a.id.AcceptInvitation(ctx, a.sender.token(t, "a2@example.test"), "pw", uuid.Nil())
	if err != nil {
		t.Fatal(err)
	}
	admin2, err := a.id.ResolveTenant(ctx, user2, "a")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("監査の記録に失敗したら業務の変更も残らない", func(t *testing.T) {
		a.id.AuditHook = func() error { return errors.New("injected") }
		err := a.id.ChangeAccessLevel(ctx, admin, adminUser, admin2.MembershipID, identity.LevelOffice)
		a.id.AuditHook = nil
		if err == nil {
			t.Fatal("失敗しなかった")
		}
		if m, _ := a.id.ResolveTenant(ctx, user2, "a"); m.Level != identity.LevelAdmin {
			t.Fatalf("変更が残った: %s", m.Level)
		}
	})

	t.Run("2人の admin が同時に互いを降格しても1人は残る", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Go(func() { errs[0] = a.id.ChangeAccessLevel(ctx, admin, adminUser, admin2.MembershipID, identity.LevelOffice) })
		wg.Go(func() { errs[1] = a.id.ChangeAccessLevel(ctx, admin2, user2, admin.MembershipID, identity.LevelOffice) })
		wg.Wait()
		if n := count(t, ctx, a.superuser, `SELECT count(*) FROM memberships WHERE access_level = 'admin' AND active`); n != 1 {
			t.Fatalf("残った admin: %d（errs: %v）", n, errs)
		}
		failed := 0
		for _, err := range errs {
			if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrLastAdmin) {
				failed++
			} else if err != nil {
				t.Errorf("想定外のエラー: %v", err)
			}
		}
		if failed != 1 {
			t.Fatalf("拒否された操作: %d, want 1（errs: %v）", failed, errs)
		}
	})
}

func TestJobErrorsHaveNoPersonalData(t *testing.T) {
	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })
	a := newAppWithLogger(t, logger)
	ctx := context.Background()
	admin, adminUser := a.newTenant(t, "a", "admin@example.test")

	a.sender.fail.Store(true)
	const canary = "canary-7f3a@example.test"
	if _, err := tenancy.Invite(ctx, a.app, a.jobs, admin, adminUser, canary, identity.LevelOffice); err != nil {
		t.Fatal(err)
	}
	// 1回目の失敗が記録されるまで待つ。
	deadline := time.Now().Add(10 * time.Second)
	for count(t, ctx, a.superuser, `SELECT count(*) FROM river_job WHERE kind = 'send_invitation' AND cardinality(errors) > 0`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("ジョブが失敗しない")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var errorsText string
	if err := a.superuser.QueryRow(ctx, `SELECT errors::text FROM river_job WHERE kind = 'send_invitation'`).Scan(&errorsText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errorsText, "canary") {
		t.Errorf("River のエラーに個人情報が残った: %s", errorsText)
	}
	if err := a.jobs.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// ワーカーのログと、River 自身の失敗のログ（v0.47 の job_executor の「Job errored」）が実際に捕まっていることを
	// 確かめてから、カナリアがないことを確かめる。
	if !strings.Contains(logs.String(), "メールの送信に失敗") {
		t.Fatalf("ワーカーのログが捕まっていない: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "Job errored") {
		t.Fatalf("River のログが捕まっていない: %s", logs.String())
	}
	if strings.Contains(logs.String(), "canary") {
		t.Errorf("ログに個人情報が残った: %s", logs.String())
	}
	t.Logf("River のエラー: %s", errorsText)
}

// syncBuffer は、ワーカーと River のゴルーチンから並行に書かれるログのバッファ。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
