-- name: CreateNote :one
INSERT INTO notes (tenant_id, body, created_by_membership_id)
VALUES (app_tenant_id(), $1, $2)
RETURNING id;

-- name: ListNotes :many
SELECT id, body FROM notes WHERE tenant_id = app_tenant_id() ORDER BY body;

-- name: TenantBySlug :one
-- 例外関数は該当がないと NULL を返すので、行を返さない形にして pgx.ErrNoRows で受ける。
SELECT t.tenant_id::uuid FROM (SELECT fn_tenant_by_slug($1) AS tenant_id) t WHERE t.tenant_id IS NOT NULL;

-- name: LoginLookup :one
SELECT user_id::uuid, hashed_password::text, credential_version::integer FROM fn_login_lookup($1) WHERE user_id IS NOT NULL;

-- name: ConsumeToken :one
SELECT subject_id::uuid FROM (SELECT fn_auth_token_consume($1, $2) AS subject_id) c WHERE c.subject_id IS NOT NULL;
