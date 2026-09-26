package spike_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tkakkie/tencle/internal/identity"
	"github.com/tkakkie/tencle/internal/mail"
	"github.com/tkakkie/tencle/internal/platform/db"
	"github.com/tkakkie/tencle/internal/tenancy"
	"github.com/tkakkie/tencle/internal/web"
)

// HTTP と Cookie を通して、ログイン・テナントルートの読み書き・別テナントの 404・無効化の即時反映を確かめる。
func TestHTTP(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	admin, adminUser := a.newTenant(t, "a", "admin@example.test")
	a.newTenant(t, "b", "b@example.test")
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

	// Secure の Cookie を送るので TLS にする。
	srv := httptest.NewTLSServer(web.Handler(a.id))
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := srv.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // テスト用のサーバー

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var b strings.Builder
		buf := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, b.String()
	}
	post := func(path string, form url.Values) *http.Response {
		t.Helper()
		resp, err := client.PostForm(srv.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}

	if code, _ := get("/t/a/notes"); code != http.StatusUnauthorized {
		t.Fatalf("未ログイン: %d", code)
	}
	resp := post("/login", url.Values{"email": {"office@example.test"}, "password": {"pw-office"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("ログイン: %d", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "tencle_session" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("Cookie の属性: %+v", cookie)
	}
	if resp := post("/t/a/notes", url.Values{"body": {"http-note"}, "tenant_id": {uuid.New().String()}}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("書き込み: %d", resp.StatusCode)
	}
	if code, body := get("/t/a/notes"); code != http.StatusOK || !strings.Contains(body, "http-note") {
		t.Fatalf("読み取り: %d %q", code, body)
	}
	codeOther, _ := get("/t/b/notes")
	codeMissing, _ := get("/t/nothing/notes")
	if codeOther != http.StatusNotFound || codeMissing != http.StatusNotFound {
		t.Fatalf("別テナント: %d、存在しない: %d（どちらも 404）", codeOther, codeMissing)
	}
	// 同じ Cookie のまま、priest への降格の後の次のリクエストは、読み取りも書き込みも 404。
	if err := a.id.ChangeAccessLevel(ctx, admin, adminUser, office.MembershipID, identity.LevelPriest); err != nil {
		t.Fatal(err)
	}
	if code, _ := get("/t/a/notes"); code != http.StatusNotFound {
		t.Fatalf("降格の後の読み取り: %d", code)
	}
	if resp := post("/t/a/notes", url.Values{"body": {"after-demote"}}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("降格の後の書き込み: %d", resp.StatusCode)
	}
	// 同じ Cookie のまま、無効化の後の次のリクエストは 404。
	if err := a.id.Deactivate(ctx, admin, adminUser, office.MembershipID); err != nil {
		t.Fatal(err)
	}
	if code, _ := get("/t/a/notes"); code != http.StatusNotFound {
		t.Fatalf("無効化の後: %d", code)
	}
}

// ログインがパスワードを検証した後、セッションを保存する前にパスワードが更新されても、そのセッションは使えない。
func TestLoginRacesPasswordReset(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	_, userID := a.newTenant(t, "a", "admin@example.test")

	var token string
	err := pgx.BeginFunc(ctx, a.app, func(tx pgx.Tx) error {
		var err error
		token, err = a.id.IssueToken(ctx, tx, identity.TokenPasswordReset, userID, userID, time.Now().Add(time.Hour))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// 実際の Login を、パスワードの検証の後・セッションの保存の前で止め、別の接続で再設定をコミットさせる。
	a.id.LoginHook = func() {
		if err := a.id.ResetPassword(ctx, token, "new"); err != nil {
			t.Errorf("再設定: %v", err)
		}
	}
	session, _, err := a.id.Login(ctx, "admin@example.test", "pw-admin@example.test")
	a.id.LoginHook = nil
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.id.Authenticate(ctx, session); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("古いパスワードで検証したセッション: %v", err)
	}
}

// 例外関数は、一時テーブルで同じ名前の表を作っても、参照先が変わらない。
func TestSearchPathIgnoresTempTables(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	a.newTenant(t, "a", "admin@example.test")
	err := pgx.BeginFunc(ctx, a.app, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE users (id uuid, email text, hashed_password text, credential_version integer) ON COMMIT DROP`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pg_temp.users VALUES (gen_random_uuid(), 'fake@example.test', 'fake', 0)`); err != nil {
			return err
		}
		var id *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT user_id FROM fn_login_lookup('fake@example.test')`).Scan(&id); err != nil {
			return err
		}
		if id != nil {
			t.Error("一時テーブルの行が例外関数から見えた")
		}
		return tx.QueryRow(ctx, `SELECT user_id FROM fn_login_lookup('admin@example.test')`).Scan(&id)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// 受諾とパスワード再設定の途中で失敗させると何も残らず、同じトークンで再試行できる。
func TestRollbackAndRetry(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	admin, adminUser := a.newTenant(t, "a", "admin@example.test")
	failOn := func(table, cond string) func() {
		t.Helper()
		for _, sql := range []string{
			`CREATE OR REPLACE FUNCTION fail_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$`,
			`CREATE TRIGGER fail_insert BEFORE INSERT ON ` + table + ` FOR EACH ROW WHEN (` + cond + `) EXECUTE FUNCTION fail_insert()`,
		} {
			if _, err := a.superuser.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
		}
		return func() {
			if _, err := a.superuser.Exec(ctx, `DROP TRIGGER fail_insert ON `+table); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("受諾", func(t *testing.T) {
		if _, err := tenancy.Invite(ctx, a.app, a.jobs, admin, adminUser, "new@example.test", identity.LevelOffice); err != nil {
			t.Fatal(err)
		}
		token := a.sender.token(t, "new@example.test")
		undo := failOn("audit_logs", `NEW.action = 'membership.created'`)
		_, err := a.id.AcceptInvitation(ctx, token, "pw", uuid.Nil())
		undo()
		if err == nil {
			t.Fatal("失敗しなかった")
		}
		for _, sql := range []string{
			`SELECT count(*) FROM users WHERE email = 'new@example.test'`,
			`SELECT count(*) FROM invitations WHERE email = 'new@example.test' AND status <> 'pending'`,
			`SELECT count(*) FROM auth_tokens WHERE kind = 'invitation' AND consumed_at IS NOT NULL`,
		} {
			if n := count(t, ctx, a.superuser, sql); n != 0 {
				t.Errorf("%s: %d", sql, n)
			}
		}
		if _, err := a.id.AcceptInvitation(ctx, token, "pw", uuid.Nil()); err != nil {
			t.Fatalf("再試行: %v", err)
		}
	})

	t.Run("パスワード再設定", func(t *testing.T) {
		session, _, err := a.id.Login(ctx, "admin@example.test", "pw-admin@example.test")
		if err != nil {
			t.Fatal(err)
		}
		var token string
		err = pgx.BeginFunc(ctx, a.app, func(tx pgx.Tx) error {
			var err error
			token, err = a.id.IssueToken(ctx, tx, identity.TokenPasswordReset, adminUser, adminUser, time.Now().Add(time.Hour))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		undo := failOn("user_events", `true`)
		err = a.id.ResetPassword(ctx, token, "changed")
		undo()
		if err == nil {
			t.Fatal("失敗しなかった")
		}
		if _, _, err := a.id.Login(ctx, "admin@example.test", "pw-admin@example.test"); err != nil {
			t.Errorf("古いパスワードでログインできない: %v", err)
		}
		if _, err := a.id.Authenticate(ctx, session); err != nil {
			t.Errorf("セッションが失効した: %v", err)
		}
		if err := a.id.ResetPassword(ctx, token, "changed"); err != nil {
			t.Fatalf("再試行: %v", err)
		}
	})
}

// 招待のメールのジョブは、処理の時点で依頼者と招待を確かめ直し、だめなら送らない（I-20）。
func TestInvitationJobRechecks(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	admin, adminUser := a.newTenant(t, "a", "a1@example.test")
	otherTenant, _ := a.newTenant(t, "b", "b@example.test")
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
	worker := &mail.InvitationWorker{Identity: a.id, Sender: a.sender}
	// ジョブを入れずに招待の行だけを作り、ワーカーを直接呼ぶ。
	newInvitation := func(email string) uuid.UUID {
		id := uuid.New()
		err := db.TenantTx(ctx, a.app, admin.TenantID, adminUser, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO invitations (tenant_id, id, email, access_level, expires_at) VALUES ($1, $2, $3, 'office', now() + interval '7 days')`, admin.TenantID, id, email)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	work := func(args mail.InvitationArgs) {
		t.Helper()
		job := &river.Job[mail.InvitationArgs]{JobRow: &rivertype.JobRow{Kind: args.Kind()}, Args: args}
		if err := worker.Work(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	noMail := func(email string) {
		t.Helper()
		select {
		case body := <-a.sender.box(email):
			t.Fatalf("%s に送られた: %s", email, body)
		case <-time.After(200 * time.Millisecond):
		}
	}

	t.Run("依頼者が無効化された", func(t *testing.T) {
		inv := newInvitation("x1@example.test")
		if err := a.id.Deactivate(ctx, admin, adminUser, admin2.MembershipID); err != nil {
			t.Fatal(err)
		}
		work(mail.InvitationArgs{Version: 1, TenantID: admin.TenantID, RequesterMembershipID: admin2.MembershipID, InvitationID: inv})
		noMail("x1@example.test")
	})
	t.Run("招待が取り消された", func(t *testing.T) {
		inv := newInvitation("x2@example.test")
		err := db.TenantTx(ctx, a.app, admin.TenantID, adminUser, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE invitations SET status = 'revoked' WHERE id = $1`, inv)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		work(mail.InvitationArgs{Version: 1, TenantID: admin.TenantID, RequesterMembershipID: admin.MembershipID, InvitationID: inv})
		noMail("x2@example.test")
	})
	t.Run("別テナントを指定した", func(t *testing.T) {
		inv := newInvitation("x3@example.test")
		work(mail.InvitationArgs{Version: 1, TenantID: otherTenant.TenantID, RequesterMembershipID: otherTenant.MembershipID, InvitationID: inv})
		noMail("x3@example.test")
	})
	t.Run("条件を満たせば送る", func(t *testing.T) {
		inv := newInvitation("x4@example.test")
		work(mail.InvitationArgs{Version: 1, TenantID: admin.TenantID, RequesterMembershipID: admin.MembershipID, InvitationID: inv})
		if tok := a.sender.token(t, "x4@example.test"); tok == "" {
			t.Fatal("トークンがない")
		}
		// トークンの期限は招待の期限と同じ。
		if n := count(t, ctx, a.superuser, `SELECT count(*) FROM auth_tokens a JOIN invitations i ON i.id = a.subject_id WHERE i.email = 'x4@example.test' AND a.expires_at = i.expires_at`); n != 1 {
			t.Errorf("トークンの期限が招待と違う: %d", n)
		}
	})
}

// ワーカーの境界で、DB のエラー（サーバーのメッセージ・クライアントの変換）からも値を取り除く。
func TestSanitize(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	const canary = "canary-4b1e"
	_, serverErr := a.app.Exec(ctx, `SELECT $1::integer`, canary)
	_, clientErr := a.app.Exec(ctx, `SELECT $1::uuid`, struct{ S string }{canary})
	for name, err := range map[string]error{"サーバー": serverErr, "クライアント": clientErr} {
		if err == nil || !strings.Contains(err.Error(), canary) {
			t.Fatalf("%s: 前提のエラーにカナリアが入っていない: %v", name, err)
		}
		if got := mail.Sanitize(err); strings.Contains(got.Error(), canary) {
			t.Errorf("%s: 変換後にカナリアが残った: %v", name, got)
		}
	}
	if got := mail.Sanitize(errors.New(canary)); strings.Contains(got.Error(), canary) {
		t.Errorf("その他のエラー: %v", got)
	}
}

// メールアドレスは入力の境界で正規化する。大文字を混ぜて招待しても、既存の User と照合でき、ログインもできる。
func TestEmailNormalization(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	admin, adminUser := a.newTenant(t, "a", "Admin@Example.test")
	if _, err := tenancy.Invite(ctx, a.app, a.jobs, admin, adminUser, " Office@Example.TEST ", identity.LevelOffice); err != nil {
		t.Fatal(err)
	}
	if _, err := a.id.AcceptInvitation(ctx, a.sender.token(t, "office@example.test"), "pw", uuid.Nil()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.id.Login(ctx, "OFFICE@example.test", "pw"); err != nil {
		t.Fatalf("大文字を混ぜたログイン: %v", err)
	}
	// 別テナントの作成で、大文字を混ぜても既存の User を再利用する。
	if _, err := tenancy.Create(ctx, a.env.creator, a.creatorJobs, "b", "b", "OFFICE@EXAMPLE.test"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, ctx, a.superuser, `SELECT count(*) FROM users WHERE lower(email) = 'office@example.test'`); n != 1 {
		t.Fatalf("User の数: %d, want 1", n)
	}
}

// パスワード設定のメールのジョブは、処理の時点で既にパスワードを設定済みなら送らない。
func TestPasswordSetupJobSkipsWhenSet(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	_, userID := a.newTenant(t, "a", "admin@example.test")
	worker := &mail.PasswordSetupWorker{Identity: a.id, Sender: a.sender}
	job := &river.Job[mail.PasswordSetupArgs]{JobRow: &rivertype.JobRow{Kind: mail.PasswordSetupArgs{}.Kind()}, Args: mail.PasswordSetupArgs{UserID: userID}}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-a.sender.box("admin@example.test"):
		t.Fatalf("設定済みなのに送った: %s", body)
	case <-time.After(200 * time.Millisecond):
	}
	if n := count(t, ctx, a.superuser, `SELECT count(*) FROM auth_tokens WHERE kind = 'password_reset' AND consumed_at IS NULL`); n != 0 {
		t.Fatalf("トークンを発行した: %d", n)
	}
}

// パスワードの更新で、未使用のパスワード再設定のトークンもすべて失効する。発行のときも前のトークンを失効させる。
func TestPasswordResetTokensRevoked(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	_, userID := a.newTenant(t, "a", "admin@example.test")
	issue := func() string {
		t.Helper()
		var token string
		err := pgx.BeginFunc(ctx, a.app, func(tx pgx.Tx) error {
			var err error
			token, err = a.id.IssueToken(ctx, tx, identity.TokenPasswordReset, userID, userID, time.Now().Add(time.Hour))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	first := issue()
	second := issue()
	if err := a.id.ResetPassword(ctx, first, "x"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("前に発行したトークン: %v", err)
	}
	if err := a.id.ResetPassword(ctx, second, "new"); err != nil {
		t.Fatal(err)
	}
	// 別の経路でパスワードが更新されたら、発行済みの再設定のトークンは使えない。
	third := issue()
	if _, err := a.app.Exec(ctx, `SELECT fn_update_password($1, $2)`, userID, "changed-elsewhere"); err != nil {
		t.Fatal(err)
	}
	if err := a.id.ResetPassword(ctx, third, "x"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("パスワードの更新の後のトークン: %v", err)
	}
}

// 未設定・パスワード違い・存在しないメールアドレスのどれでも、同じエラーで、argon2id の検証を通る。
func TestLoginTimingIsUniform(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	a.newTenant(t, "a", "admin@example.test")
	// パスワードを設定していない User（別テナントの作成で作り、メールは使わない）。
	if _, err := tenancy.Create(ctx, a.env.creator, a.creatorJobs, "b", "b", "unset@example.test"); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"unset@example.test", "admin@example.test", "nobody@example.test"} {
		start := time.Now()
		_, _, err := a.id.Login(ctx, email, "wrong")
		d := time.Since(start)
		if !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Errorf("%s: %v", email, err)
		}
		// argon2id（19 MiB、反復2）の検証は数ミリ秒以上かかる。即座に失敗していれば検証を飛ばしている。
		if d < 3*time.Millisecond {
			t.Errorf("%s: %v で終わった（検証を飛ばしている）", email, d)
		}
	}
}

// 取り消した招待は、未使用のトークンでも受諾できない。
func TestRevokedInvitationCannotBeAccepted(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	admin, adminUser := a.newTenant(t, "a", "admin@example.test")
	inv, err := tenancy.Invite(ctx, a.app, a.jobs, admin, adminUser, "late@example.test", identity.LevelOffice)
	if err != nil {
		t.Fatal(err)
	}
	token := a.sender.token(t, "late@example.test")
	err = db.TenantTx(ctx, a.app, admin.TenantID, adminUser, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE invitations SET status = 'revoked' WHERE id = $1`, inv)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.id.AcceptInvitation(ctx, token, "pw", uuid.Nil()); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("取り消した招待の受諾: %v", err)
	}
	// 監査の Actor：テナントの作成は System（create-tenant）、招待の作成は依頼した Membership。
	if n := count(t, ctx, a.superuser, `SELECT count(*) FROM audit_logs WHERE action = 'tenant.created' AND actor_kind = 'system' AND actor_system = 'create-tenant' AND actor_membership_id IS NULL`); n != 1 {
		t.Errorf("テナントの作成の Actor: %d", n)
	}
	if n := count(t, ctx, a.superuser, `SELECT count(*) FROM audit_logs WHERE action = 'invitation.created' AND target_id = '`+inv.String()+`' AND actor_kind = 'membership' AND actor_membership_id = '`+admin.MembershipID.String()+`'`); n != 1 {
		t.Errorf("招待の作成の Actor: %d", n)
	}
}
