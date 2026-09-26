-- name: CreateNote :one
INSERT INTO notes (tenant_id, body, created_by_membership_id)
VALUES (app_tenant_id(), $1, $2)
RETURNING id;

-- name: ListNotes :many
SELECT id, body FROM notes WHERE tenant_id = app_tenant_id() ORDER BY body;

-- name: TenantBySlug :one
SELECT fn_tenant_by_slug($1)::uuid AS tenant_id;
