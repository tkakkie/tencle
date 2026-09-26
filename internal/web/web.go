// Package web は、スパイクの最小限の HTTP の層。Cookie のセッションと、テナントルートの解決を確かめる。
package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/tkakkie/tencle/internal/identity"
	"github.com/tkakkie/tencle/internal/platform/db"
)

const sessionCookie = "tencle_session"

// Handler はルート表。ルートの種類（認証・テナント）はスパイクでは手で分けている（本番は #11 のルート表）。
func Handler(id *identity.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		token, _, err := id.Login(r.Context(), r.PostFormValue("email"), r.PostFormValue("password"))
		if errors.Is(err, identity.ErrInvalidCredentials) {
			http.Error(w, "メールアドレスかパスワードが違います", http.StatusUnauthorized)
			return
		}
		if err != nil {
			http.Error(w, "エラー", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: token, Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
			MaxAge: int(id.SessionTTL.Seconds()),
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /t/{slug}/notes", tenantRoute(id, func(w http.ResponseWriter, r *http.Request, m identity.Membership, userID uuid.UUID) {
		var bodies []string
		err := db.TenantTx(r.Context(), id.Pool, m.TenantID, userID, func(tx pgx.Tx) error {
			rows, err := tx.Query(r.Context(), `SELECT body FROM notes ORDER BY body`)
			if err != nil {
				return err
			}
			bodies, err = pgx.CollectRows(rows, pgx.RowTo[string])
			return err
		})
		if err != nil {
			http.Error(w, "エラー", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, strings.Join(bodies, "\n"))
	}))
	mux.HandleFunc("POST /t/{slug}/notes", tenantRoute(id, func(w http.ResponseWriter, r *http.Request, m identity.Membership, userID uuid.UUID) {
		// tenant_id はボディから取らず、URL で決めたテナントを使う（I-5）。
		err := db.TenantTx(r.Context(), id.Pool, m.TenantID, userID, func(tx pgx.Tx) error {
			_, err := tx.Exec(r.Context(), `INSERT INTO notes (tenant_id, body, created_by_membership_id) VALUES ($1, $2, $3)`, m.TenantID, r.PostFormValue("body"), m.MembershipID)
			return err
		})
		if err != nil {
			http.Error(w, "エラー", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	return mux
}

// tenantRoute は、毎回のリクエストでセッションと Membership を DB で確かめる（I-8）。
// 未ログインは 401、存在しない slug と Membership のない slug は同じ 404（I-5）。
func tenantRoute(id *identity.Service, next func(http.ResponseWriter, *http.Request, identity.Membership, uuid.UUID)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Error(w, "ログインしてください", http.StatusUnauthorized)
			return
		}
		userID, err := id.Authenticate(r.Context(), c.Value)
		if err != nil {
			http.Error(w, "ログインしてください", http.StatusUnauthorized)
			return
		}
		m, err := id.ResolveTenant(r.Context(), userID, r.PathValue("slug"))
		if errors.Is(err, identity.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		// スパイクでは、ノートは office 以上だけとする。priest 段階の範囲外は 404（I-7）。
		// 本番では、ハンドラではなく Actor を受け取るユースケースで認可する（I-6・I-23）。
		if m.Level == identity.LevelPriest {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "エラー", http.StatusInternalServerError)
			return
		}
		next(w, r, m, userID)
	}
}
