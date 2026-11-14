-- name: Ping :one
SELECT 1::int AS ok;

-- name: InsertAuditLog :exec
INSERT INTO audit_log (org_id, actor_id, actor_kind, entity, entity_id, action, before, after)
VALUES (
    @org_id,
    sqlc.narg('actor_id'),
    @actor_kind,
    @entity,
    sqlc.narg('entity_id'),
    @action,
    sqlc.narg('before')::jsonb,
    sqlc.narg('after')::jsonb
);
