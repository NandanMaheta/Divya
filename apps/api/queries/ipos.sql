-- IPO catalogue (feature 008). Every query filters by org_id. Instants come from @now.

-- name: InsertIPO :one
INSERT INTO ipos (
    org_id, name, exchanges, nse_symbol, issue_size_paise, price_band_floor_paise, price_band_cap_paise,
    issue_price_paise, lot_size, open_date, close_date, allotment_date, refund_date, demat_credit_date,
    listing_date, registrar, lead_managers, offer_document_url, create_idempotency_key,
    status_changed_at, created_at, updated_at
) VALUES (
    @org_id, @name, @exchanges::text[], sqlc.narg('nse_symbol'), sqlc.narg('issue_size_paise'),
    sqlc.narg('price_band_floor_paise'), sqlc.narg('price_band_cap_paise'), sqlc.narg('issue_price_paise'),
    sqlc.narg('lot_size'), sqlc.narg('open_date'), sqlc.narg('close_date'), sqlc.narg('allotment_date'),
    sqlc.narg('refund_date'), sqlc.narg('demat_credit_date'), sqlc.narg('listing_date'),
    sqlc.narg('registrar'), @lead_managers::text[], sqlc.narg('offer_document_url'),
    sqlc.narg('create_idempotency_key'), @now, @now, @now
)
ON CONFLICT (org_id, create_idempotency_key) WHERE create_idempotency_key IS NOT NULL DO NOTHING
RETURNING *;

-- name: GetIPOByCreateKey :one
SELECT * FROM ipos WHERE org_id = @org_id AND create_idempotency_key = @key;

-- name: GetIPO :one
SELECT * FROM ipos WHERE org_id = @org_id AND id = @id;

-- name: GetIPOForUpdate :one
SELECT * FROM ipos WHERE org_id = @org_id AND id = @id FOR UPDATE;

-- name: GetIPOForShare :one
SELECT * FROM ipos WHERE org_id = @org_id AND id = @id FOR SHARE;

-- name: GetPublishedIPO :one
SELECT * FROM ipos WHERE org_id = @org_id AND id = @id AND published;

-- name: UpdateIPOFacts :one
UPDATE ipos SET
    name = @name,
    exchanges = @exchanges::text[],
    nse_symbol = sqlc.narg('nse_symbol'),
    issue_size_paise = sqlc.narg('issue_size_paise'),
    price_band_floor_paise = sqlc.narg('price_band_floor_paise'),
    price_band_cap_paise = sqlc.narg('price_band_cap_paise'),
    issue_price_paise = sqlc.narg('issue_price_paise'),
    lot_size = sqlc.narg('lot_size'),
    open_date = sqlc.narg('open_date'),
    close_date = sqlc.narg('close_date'),
    allotment_date = sqlc.narg('allotment_date'),
    refund_date = sqlc.narg('refund_date'),
    demat_credit_date = sqlc.narg('demat_credit_date'),
    listing_date = sqlc.narg('listing_date'),
    registrar = sqlc.narg('registrar'),
    lead_managers = @lead_managers::text[],
    offer_document_url = sqlc.narg('offer_document_url'),
    version = version + 1,
    updated_at = @now
WHERE org_id = @org_id AND id = @id AND version = @version
RETURNING *;

-- name: SetIPOFlag :one
UPDATE ipos SET
    apply_flag = sqlc.narg('apply_flag'),
    flag_note = sqlc.narg('flag_note'),
    version = version + 1,
    updated_at = @now
WHERE org_id = @org_id AND id = @id AND version = @version
RETURNING *;

-- name: SetIPOPublished :one
UPDATE ipos SET
    published = @published,
    first_published_at = CASE WHEN @published::boolean THEN COALESCE(first_published_at, @now::timestamptz)
                              ELSE first_published_at END,
    version = version + 1,
    updated_at = @now
WHERE org_id = @org_id AND id = @id AND version = @version
RETURNING *;

-- name: SetIPOStatus :one
UPDATE ipos SET
    status = @status,
    status_source = @status_source,
    status_changed_at = @now,
    last_override_at = COALESCE(sqlc.narg('override_at')::timestamptz, last_override_at),
    version = version + 1,
    updated_at = @now
WHERE org_id = @org_id AND id = @id AND version = @version
RETURNING *;

-- name: DeleteIPO :execrows
DELETE FROM ipos
WHERE org_id = @org_id AND id = @id AND version = @version AND first_published_at IS NULL;

-- name: ListIPOsAdmin :many
SELECT * FROM ipos
WHERE org_id = @org_id
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('published')::boolean IS NULL OR published = sqlc.narg('published'))
  AND (sqlc.narg('flag')::text IS NULL
       OR (sqlc.narg('flag') = 'NONE' AND apply_flag IS NULL)
       OR apply_flag = sqlc.narg('flag'))
  AND (sqlc.narg('month_start')::date IS NULL OR open_date >= sqlc.narg('month_start'))
  AND (sqlc.narg('month_end')::date IS NULL OR open_date <= sqlc.narg('month_end'))
  AND (NOT @cursor_set::boolean
       OR (@cursor_null::boolean AND (open_date IS NOT NULL OR id < @cursor_id::uuid))
       OR (NOT @cursor_null::boolean AND open_date IS NOT NULL
           AND (open_date < @cursor_open::date OR (open_date = @cursor_open::date AND id < @cursor_id::uuid))))
ORDER BY open_date DESC NULLS FIRST, id DESC
LIMIT @row_limit;

-- name: ListPublishedIPOs :many
SELECT * FROM ipos
WHERE org_id = @org_id AND published
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
  AND (NOT @cursor_set::boolean
       OR (@cursor_null::boolean AND (open_date IS NOT NULL OR id < @cursor_id::uuid))
       OR (NOT @cursor_null::boolean AND open_date IS NOT NULL
           AND (open_date < @cursor_open::date OR (open_date = @cursor_open::date AND id < @cursor_id::uuid))))
ORDER BY open_date DESC NULLS FIRST, id DESC
LIMIT @row_limit;

-- name: FindIPOAuditByIdempotencyKey :one
SELECT id FROM audit_log
WHERE org_id = @org_id AND entity = 'ipo' AND entity_id = @entity_id AND action = @action
  AND COALESCE(after, before) ->> 'idempotency_key' = @key::text
LIMIT 1;
