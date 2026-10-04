-- +goose Up
CREATE TABLE outings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id),
    initiator_id BIGINT NOT NULL REFERENCES users(id),
    timezone TEXT NOT NULL,
    duration_minutes INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE outing_dates (
    outing_id BIGINT NOT NULL REFERENCES outings(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    PRIMARY KEY (outing_id, date)
);
CREATE TABLE outing_decisions (
    outing_id BIGINT NOT NULL REFERENCES outings(id) ON DELETE CASCADE,
    member_id BIGINT NOT NULL REFERENCES users(id),
    choice TEXT NOT NULL CHECK (choice IN ('accept', 'deny')),
    max_budget_fen BIGINT,
    requires_wheelchair_access BOOLEAN,
    PRIMARY KEY (outing_id, member_id),
    CHECK ((choice = 'accept' AND max_budget_fen IS NOT NULL AND requires_wheelchair_access IS NOT NULL)
        OR (choice = 'deny' AND max_budget_fen IS NULL AND requires_wheelchair_access IS NULL))
);
CREATE TABLE outing_availability (
    outing_id BIGINT NOT NULL,
    member_id BIGINT NOT NULL,
    date DATE NOT NULL,
    start_minute SMALLINT NOT NULL,
    end_minute SMALLINT NOT NULL,
    PRIMARY KEY (outing_id, member_id, date, start_minute),
    FOREIGN KEY (outing_id, member_id) REFERENCES outing_decisions(outing_id, member_id) ON DELETE CASCADE
);
CREATE TABLE outing_arrangements (
    outing_id BIGINT PRIMARY KEY REFERENCES outings(id) ON DELETE CASCADE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    place TEXT NOT NULL,
    activity TEXT NOT NULL
);
CREATE TABLE outing_confirmations (
    outing_id BIGINT NOT NULL REFERENCES outing_arrangements(outing_id) ON DELETE CASCADE,
    member_id BIGINT NOT NULL REFERENCES users(id),
    choice TEXT CHECK (choice IN ('accept', 'deny')),
    PRIMARY KEY (outing_id, member_id)
);
CREATE INDEX outing_decisions_member ON outing_decisions(member_id, outing_id);
CREATE INDEX outing_confirmations_member ON outing_confirmations(member_id, outing_id);
CREATE INDEX outings_group ON outings(group_id, id);

-- Only input completeness is enforced across rows. Candidate-date changes may
-- leave old availability outside the plan; this is a readable business warning.
-- +goose StatementBegin
CREATE FUNCTION outing_input_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target BIGINT; member BIGINT; response TEXT; n INTEGER;
BEGIN
    FOR target, member IN
        SELECT OLD.outing_id, OLD.member_id WHERE TG_OP <> 'INSERT'
        UNION
        SELECT NEW.outing_id, NEW.member_id WHERE TG_OP <> 'DELETE'
        ORDER BY 1, 2
    LOOP
        -- Serialize completeness checks, including writes outside the Go service.
        SELECT choice INTO response FROM outing_decisions WHERE outing_id = target AND member_id = member FOR UPDATE;
        IF NOT FOUND THEN CONTINUE; END IF;
        SELECT count(*) INTO n FROM outing_availability WHERE outing_id = target AND member_id = member;
        IF (response = 'accept' AND n = 0) OR (response = 'deny' AND n <> 0) THEN
            RAISE EXCEPTION 'incomplete decision input' USING ERRCODE = '23514', CONSTRAINT = 'outing_input_complete';
        END IF;
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER outing_input_complete AFTER INSERT OR UPDATE OR DELETE ON outing_decisions
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION outing_input_complete();
CREATE CONSTRAINT TRIGGER outing_input_complete AFTER INSERT OR UPDATE OR DELETE ON outing_availability
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION outing_input_complete();

-- +goose Down
DROP TABLE outing_confirmations, outing_arrangements, outing_availability, outing_decisions, outing_dates, outings;
DROP FUNCTION outing_input_complete();
