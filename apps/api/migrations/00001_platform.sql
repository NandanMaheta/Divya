-- +goose Up
-- Organisations make the schema multi-tenant ready from day one (one org today: the operator).
CREATE TABLE orgs (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Append-only audit trail for every change made by anyone (BR: full history).
CREATE TABLE audit_log (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid NOT NULL REFERENCES orgs (id),
    actor_id    uuid,
    actor_kind  text NOT NULL CHECK (actor_kind IN ('admin', 'client', 'system')),
    entity      text NOT NULL,
    entity_id   uuid,
    action      text NOT NULL,
    before      jsonb,
    after       jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_entity_idx ON audit_log (org_id, entity, entity_id, created_at DESC);

-- Audit rows can never be changed or removed.
-- +goose StatementBegin
CREATE FUNCTION forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER audit_log_append_only BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION forbid_mutation();

-- +goose Down
DROP TABLE audit_log;
DROP FUNCTION forbid_mutation();
DROP TABLE orgs;
