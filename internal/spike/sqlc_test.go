package spike_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/tkakkie/tencle/internal/platform/db"
	"github.com/tkakkie/tencle/internal/spike/notestore"
)

// sqlc で生成したコードが、TenantTx と同じトランザクションで使え、RLS が効くこと。
func TestSQLCInTenantTx(t *testing.T) {
	e := newEnv(t)
	s := seedTwoTenants(t, e)
	ctx := context.Background()
	err := db.TenantTx(ctx, e.app, s.tenantA, s.userA, func(tx pgx.Tx) error {
		q := notestore.New(tx)
		if _, err := q.CreateNote(ctx, notestore.CreateNoteParams{Body: "sqlc-a", CreatedByMembershipID: s.memberA}); err != nil {
			return err
		}
		notes, err := q.ListNotes(ctx)
		if err != nil {
			return err
		}
		if len(notes) != 2 {
			t.Errorf("A の notes: %d, want 2（seed の1件と作った1件）", len(notes))
		}
		for _, n := range notes {
			if n.Body == "note-b" {
				t.Error("B の note が見えた")
			}
		}
		id, err := q.TenantBySlug(ctx, "b")
		if err != nil || id != s.tenantB {
			t.Errorf("例外関数: %v %v", id, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// 例外関数の「該当なし」（NULL）を、生成したコードで pgx.ErrNoRows として受けられること。
func TestSQLCExceptionFunctions(t *testing.T) {
	e := newEnv(t)
	seedTwoTenants(t, e)
	ctx := context.Background()
	err := pgx.BeginFunc(ctx, e.app, func(tx pgx.Tx) error {
		q := notestore.New(tx)
		if _, err := q.TenantBySlug(ctx, "nothing"); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("存在しない slug: %v", err)
		}
		if _, err := q.LoginLookup(ctx, "nobody@example.test"); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("存在しないメールアドレス: %v", err)
		}
		row, err := q.LoginLookup(ctx, "a@example.test")
		if err != nil || row.HashedPassword != "secret-a" {
			t.Errorf("ログインの照合: %+v %v", row, err)
		}
		// 期限内・未消費のトークンは1回だけ消費でき、2回目は該当なし。
		if _, err := e.superuser.Exec(ctx, `INSERT INTO auth_tokens (token_hash, kind, subject_id, expires_at) VALUES ('\x09', 'invitation', gen_random_uuid(), now() + interval '1 hour')`); err != nil {
			return err
		}
		if _, err := q.ConsumeToken(ctx, notestore.ConsumeTokenParams{PTokenHash: []byte{0x09}, PKind: "invitation"}); err != nil {
			t.Errorf("1回目の消費: %v", err)
		}
		if _, err := q.ConsumeToken(ctx, notestore.ConsumeTokenParams{PTokenHash: []byte{0x09}, PKind: "invitation"}); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("2回目の消費: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
