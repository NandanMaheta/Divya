-- +goose Up
-- IPO catalogue (feature 008). Mutable reference data: history lives in the append-only audit_log.
-- Expand-only: one new table, one trigger function, one trigger. Instants come from the app clock.

CREATE TABLE ipos (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id                 uuid NOT NULL REFERENCES orgs (id),
    name                   text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 200),
    board                  text NOT NULL DEFAULT 'MAINBOARD' CHECK (board = 'MAINBOARD'),
    exchanges              text[] NOT NULL
        CHECK (cardinality(exchanges) BETWEEN 1 AND 2
               AND exchanges <@ ARRAY['NSE', 'BSE']::text[]
               AND (cardinality(exchanges) = 1 OR exchanges[1] <> exchanges[2])),
    nse_symbol             text CHECK (nse_symbol ~ '^[A-Z0-9&-]{1,20}$'),
    issue_size_paise       bigint CHECK (issue_size_paise BETWEEN 1 AND 1000000000000000),
    price_band_floor_paise bigint CHECK (price_band_floor_paise BETWEEN 1 AND 1000000000),
    price_band_cap_paise   bigint CHECK (price_band_cap_paise BETWEEN 1 AND 1000000000),
    issue_price_paise      bigint CHECK (issue_price_paise BETWEEN 1 AND 1000000000),
    lot_size               integer CHECK (lot_size BETWEEN 1 AND 1000000),
    open_date              date,
    close_date             date,
    allotment_date         date,
    refund_date            date,
    demat_credit_date      date,
    listing_date           date,
    registrar              text CHECK (char_length(registrar) BETWEEN 1 AND 200),
    lead_managers          text[] NOT NULL DEFAULT '{}' CHECK (cardinality(lead_managers) <= 20),
    offer_document_url     text CHECK (offer_document_url ~ '^https://' AND char_length(offer_document_url) <= 2000),
    apply_flag             text CHECK (apply_flag IN ('APPLY', 'DO_NOT_APPLY')),
    flag_note              text CHECK (char_length(flag_note) BETWEEN 1 AND 500),
    published              boolean NOT NULL DEFAULT false,
    first_published_at     timestamptz,
    status                 text NOT NULL DEFAULT 'UPCOMING'
        CHECK (status IN ('UPCOMING', 'OPEN', 'CLOSED', 'ALLOTMENT_OUT', 'LISTED', 'SETTLED')),
    status_source          text NOT NULL DEFAULT 'schedule'
        CHECK (status_source IN ('schedule', 'admin', 'settlement')),
    status_changed_at      timestamptz NOT NULL,
    last_override_at       timestamptz,
    source                 text NOT NULL DEFAULT 'manual'
        CHECK (source = 'manual' OR source ~ '^import:[a-z0-9_]{1,32}$'),
    source_ref             text CHECK (char_length(source_ref) BETWEEN 1 AND 200),
    last_synced_at         timestamptz,
    field_sources          jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(field_sources) = 'object'),
    create_idempotency_key text CHECK (char_length(create_idempotency_key) BETWEEN 8 AND 200),
    version                integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT ipos_band_pair CHECK ((price_band_floor_paise IS NULL) = (price_band_cap_paise IS NULL)),
    CONSTRAINT ipos_band_order CHECK (price_band_floor_paise <= price_band_cap_paise),
    CONSTRAINT ipos_flag_note CHECK (apply_flag IS NOT NULL OR flag_note IS NULL),
    CONSTRAINT ipos_first_published CHECK (NOT published OR first_published_at IS NOT NULL),
    CONSTRAINT ipos_source_ref CHECK ((source = 'manual') = (source_ref IS NULL)),
    CONSTRAINT ipos_last_synced CHECK (source <> 'manual' OR last_synced_at IS NULL),
    CONSTRAINT ipos_open_close CHECK (open_date <= close_date),
    CONSTRAINT ipos_close_allotment CHECK (close_date < allotment_date),
    CONSTRAINT ipos_allotment_refund CHECK (allotment_date <= refund_date),
    CONSTRAINT ipos_refund_listing CHECK (refund_date <= listing_date),
    CONSTRAINT ipos_allotment_demat CHECK (allotment_date <= demat_credit_date),
    CONSTRAINT ipos_demat_listing CHECK (demat_credit_date <= listing_date),
    CONSTRAINT ipos_allotment_listing CHECK (allotment_date < listing_date),
    CONSTRAINT ipos_publish_complete CHECK (
        NOT published OR (apply_flag IS NOT NULL AND open_date IS NOT NULL AND close_date IS NOT NULL
            AND lot_size IS NOT NULL AND (issue_price_paise IS NOT NULL OR price_band_cap_paise IS NOT NULL)))
);

CREATE INDEX ipos_admin_list_idx ON ipos (org_id, open_date DESC NULLS FIRST, id DESC);
CREATE INDEX ipos_published_list_idx ON ipos (org_id, open_date DESC NULLS FIRST, id DESC) WHERE published;
CREATE UNIQUE INDEX ipos_source_ref_uq ON ipos (org_id, source, source_ref) WHERE source_ref IS NOT NULL;
CREATE UNIQUE INDEX ipos_create_idem_uq ON ipos (org_id, create_idempotency_key)
    WHERE create_idempotency_key IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION ipos_forbid_published_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.first_published_at IS NOT NULL THEN
        RAISE EXCEPTION 'published IPOs can only be unpublished';
    END IF;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER ipos_forbid_published_delete
    BEFORE DELETE ON ipos
    FOR EACH ROW EXECUTE FUNCTION ipos_forbid_published_delete();

-- +goose Down
DROP TRIGGER ipos_forbid_published_delete ON ipos;
DROP FUNCTION ipos_forbid_published_delete();
DROP TABLE ipos;
