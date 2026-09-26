package spike_test

import (
	"context"
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
