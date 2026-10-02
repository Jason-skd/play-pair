-- +goose Up
CREATE TABLE outings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id),
    initiator_id BIGINT NOT NULL,
    timezone TEXT NOT NULL,
    duration_minutes INTEGER NOT NULL CHECK (duration_minutes BETWEEN 30 AND 1440 AND duration_minutes % 30 = 0),
    creation_key UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT outings_initiator_fkey FOREIGN KEY (group_id, initiator_id) REFERENCES group_members(group_id, member_id),
    CONSTRAINT outings_creation_key UNIQUE (initiator_id, creation_key)
);
CREATE TABLE outing_dates (
    outing_id BIGINT NOT NULL REFERENCES outings(id),
    date DATE NOT NULL CHECK (date BETWEEN DATE '0001-01-01' AND DATE '9999-12-31'),
    PRIMARY KEY (outing_id, date)
);
CREATE TABLE outing_arrangements (
    outing_id BIGINT PRIMARY KEY REFERENCES outings(id),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    place TEXT NOT NULL CHECK (btrim(place) <> ''),
    activity TEXT NOT NULL CHECK (btrim(activity) <> ''),
    CHECK (isfinite(starts_at) AND isfinite(ends_at) AND starts_at < ends_at)
);
CREATE TABLE outing_invitations (
    outing_id BIGINT NOT NULL REFERENCES outings(id),
    member_id BIGINT NOT NULL REFERENCES users(id),
    phase TEXT NOT NULL CHECK (phase IN ('decision', 'confirmation')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected', 'declined')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    accepted_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (outing_id, member_id, phase),
    CHECK ((status = 'accepted') = (accepted_at IS NOT NULL))
);
CREATE INDEX outing_invitations_inbox ON outing_invitations(member_id, outing_id, phase);
CREATE TABLE outing_member_inputs (
    outing_id BIGINT NOT NULL,
    member_id BIGINT NOT NULL,
    phase TEXT NOT NULL DEFAULT 'decision' CHECK (phase = 'decision'),
    max_budget_fen BIGINT NOT NULL CHECK (max_budget_fen >= 0),
    requires_wheelchair_access BOOLEAN NOT NULL,
    PRIMARY KEY (outing_id, member_id),
    FOREIGN KEY (outing_id, member_id, phase) REFERENCES outing_invitations(outing_id, member_id, phase)
);
CREATE TABLE outing_availability (
    outing_id BIGINT NOT NULL,
    member_id BIGINT NOT NULL,
    date DATE NOT NULL,
    start_minute SMALLINT NOT NULL,
    end_minute SMALLINT NOT NULL,
    PRIMARY KEY (outing_id, member_id, date, start_minute),
    FOREIGN KEY (outing_id, member_id) REFERENCES outing_member_inputs(outing_id, member_id),
    FOREIGN KEY (outing_id, date) REFERENCES outing_dates(outing_id, date),
    CHECK (start_minute >= 0 AND start_minute < end_minute AND end_minute <= 1440 AND start_minute % 30 = 0 AND end_minute % 30 = 0)
);

-- Every writer uses the same lock order as group joining. Cutoff is checked
-- after waiting for locks, never against the transaction-start timestamp.
-- +goose StatementBegin
CREATE FUNCTION outing_lock(target BIGINT) RETURNS void LANGUAGE plpgsql AS $$
DECLARE g BIGINT;
BEGIN
    SELECT group_id INTO g FROM outings WHERE id = target;
    PERFORM id FROM groups WHERE id = g FOR UPDATE;
    PERFORM id FROM outings WHERE id = target FOR UPDATE;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION outing_check_phase(target BIGINT, requested TEXT) RETURNS void LANGUAGE plpgsql AS $$
DECLARE start_time TIMESTAMPTZ;
BEGIN
    PERFORM outing_lock(target);
    SELECT starts_at INTO start_time FROM outing_arrangements WHERE outing_id = target;
    IF start_time <= clock_timestamp() THEN
        RAISE EXCEPTION 'outing archived' USING ERRCODE = 'P0001', CONSTRAINT = 'outing_archived';
    END IF;
    IF (start_time IS NULL AND requested <> 'decision') OR (start_time IS NOT NULL AND requested <> 'confirmation') THEN
        RAISE EXCEPTION 'phase conflict' USING ERRCODE = 'P0001', CONSTRAINT = 'outing_phase';
    END IF;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION outings_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN RAISE EXCEPTION 'outing is immutable' USING ERRCODE = '23514'; END IF;
    IF NEW.timezone = 'Local' OR NOT EXISTS (SELECT 1 FROM pg_timezone_names WHERE name = NEW.timezone) THEN
        RAISE EXCEPTION 'invalid timezone' USING ERRCODE = '23514', CONSTRAINT = 'outings_timezone';
    END IF;
    PERFORM id FROM groups WHERE id = NEW.group_id FOR UPDATE;
    NEW.created_at := clock_timestamp();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER outings_guard BEFORE INSERT OR UPDATE OR DELETE ON outings FOR EACH ROW EXECUTE FUNCTION outings_guard();

-- +goose StatementBegin
CREATE FUNCTION outing_dates_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN RAISE EXCEPTION 'dates are immutable' USING ERRCODE = '23514'; END IF;
    PERFORM outing_check_phase(NEW.outing_id, 'decision');
    IF EXISTS (SELECT 1 FROM outing_invitations WHERE outing_id = NEW.outing_id) THEN
        RAISE EXCEPTION 'dates already published' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER outing_dates_guard BEFORE INSERT OR UPDATE OR DELETE ON outing_dates FOR EACH ROW EXECUTE FUNCTION outing_dates_guard();
-- +goose StatementBegin
CREATE FUNCTION outing_dates_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n INTEGER;
BEGIN
    SELECT count(*) INTO n FROM outing_dates WHERE outing_id = NEW.id;
    IF n < 1 OR n > 31 THEN RAISE EXCEPTION 'dates required' USING ERRCODE = '23514', CONSTRAINT = 'outing_dates_complete'; END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER outing_dates_complete AFTER INSERT ON outings DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION outing_dates_complete();

-- +goose StatementBegin
CREATE FUNCTION outing_invitation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN RAISE EXCEPTION 'invitations cannot be deleted' USING ERRCODE = '23514'; END IF;
    PERFORM outing_check_phase(NEW.outing_id, NEW.phase);
    IF NOT EXISTS (SELECT 1 FROM outings o JOIN group_members m ON m.group_id = o.group_id WHERE o.id = NEW.outing_id AND m.member_id = NEW.member_id) THEN
        RAISE EXCEPTION 'membership required' USING ERRCODE = '23514', CONSTRAINT = 'outing_invitation_member';
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'pending' THEN RAISE EXCEPTION 'invitation starts pending' USING ERRCODE = '23514'; END IF;
        NEW.version := 1; NEW.accepted_at := NULL; NEW.updated_at := clock_timestamp();
    ELSE
        IF (NEW.outing_id, NEW.member_id, NEW.phase) IS DISTINCT FROM (OLD.outing_id, OLD.member_id, OLD.phase) THEN
            RAISE EXCEPTION 'invitation identity immutable' USING ERRCODE = '23514';
        END IF;
        IF NEW.status = 'pending' AND OLD.status <> 'pending' OR NEW.status = 'declined' AND OLD.status NOT IN ('accepted','declined') OR NEW.status = 'rejected' AND OLD.status IN ('accepted','declined') THEN
            RAISE EXCEPTION 'invalid response transition' USING ERRCODE = 'P0001', CONSTRAINT = 'outing_phase';
        END IF;
        IF NEW IS NOT DISTINCT FROM OLD THEN RETURN OLD; END IF;
        NEW.version := OLD.version + 1;
        NEW.updated_at := greatest(OLD.updated_at, clock_timestamp());
        NEW.accepted_at := CASE WHEN NEW.status <> 'accepted' THEN NULL WHEN OLD.status = 'accepted' THEN OLD.accepted_at ELSE clock_timestamp() END;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER outing_invitation_guard BEFORE INSERT OR UPDATE OR DELETE ON outing_invitations FOR EACH ROW EXECUTE FUNCTION outing_invitation_guard();

-- Inputs belong only to decision invitations. Changes touch the invitation;
-- deferred checks allow atomic replacement without exposing incomplete inputs.
-- +goose StatementBegin
CREATE FUNCTION outing_input_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target BIGINT;
BEGIN
    target := CASE WHEN TG_OP = 'DELETE' THEN OLD.outing_id ELSE NEW.outing_id END;
    PERFORM outing_check_phase(target, 'decision');
    IF TG_OP = 'UPDATE' AND (NEW.outing_id, NEW.member_id) IS DISTINCT FROM (OLD.outing_id, OLD.member_id) THEN
        RAISE EXCEPTION 'input identity immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER outing_input_guard BEFORE INSERT OR UPDATE OR DELETE ON outing_member_inputs FOR EACH ROW EXECUTE FUNCTION outing_input_guard();
CREATE TRIGGER outing_availability_guard BEFORE INSERT OR UPDATE OR DELETE ON outing_availability FOR EACH ROW EXECUTE FUNCTION outing_input_guard();
-- +goose StatementBegin
CREATE FUNCTION outing_input_touch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target BIGINT; member BIGINT;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NULL; END IF;
    target := CASE WHEN TG_OP = 'DELETE' THEN OLD.outing_id ELSE NEW.outing_id END;
    member := CASE WHEN TG_OP = 'DELETE' THEN OLD.member_id ELSE NEW.member_id END;
    UPDATE outing_invitations SET version = version + 1 WHERE outing_id = target AND member_id = member AND phase = 'decision';
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER outing_input_touch AFTER INSERT OR UPDATE OR DELETE ON outing_member_inputs FOR EACH ROW EXECUTE FUNCTION outing_input_touch();
CREATE TRIGGER outing_availability_touch AFTER INSERT OR UPDATE OR DELETE ON outing_availability FOR EACH ROW EXECUTE FUNCTION outing_input_touch();
-- +goose StatementBegin
CREATE FUNCTION outing_input_complete() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target BIGINT; member BIGINT; response TEXT; has_input BOOLEAN; n INTEGER;
BEGIN
    target := CASE WHEN TG_OP = 'DELETE' THEN OLD.outing_id ELSE NEW.outing_id END;
    member := CASE WHEN TG_OP = 'DELETE' THEN OLD.member_id ELSE NEW.member_id END;
    SELECT status INTO response FROM outing_invitations WHERE outing_id = target AND member_id = member AND phase = 'decision';
    SELECT EXISTS (SELECT 1 FROM outing_member_inputs WHERE outing_id = target AND member_id = member) INTO has_input;
    SELECT count(*) INTO n FROM outing_availability WHERE outing_id = target AND member_id = member;
    IF (response = 'accepted' AND (NOT has_input OR n < 1 OR n > 256)) OR (response IS DISTINCT FROM 'accepted' AND (has_input OR n <> 0)) THEN
        RAISE EXCEPTION 'incomplete input' USING ERRCODE = '23514', CONSTRAINT = 'outing_input_complete';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER outing_input_complete AFTER INSERT OR UPDATE OR DELETE ON outing_invitations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION outing_input_complete();
CREATE CONSTRAINT TRIGGER outing_input_complete AFTER INSERT OR UPDATE OR DELETE ON outing_member_inputs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION outing_input_complete();
CREATE CONSTRAINT TRIGGER outing_input_complete AFTER INSERT OR UPDATE OR DELETE ON outing_availability DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION outing_input_complete();

-- +goose StatementBegin
CREATE FUNCTION outing_arrangement_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN RAISE EXCEPTION 'arrangement immutable' USING ERRCODE = '23514'; END IF;
    PERFORM outing_check_phase(NEW.outing_id, 'decision');
    IF NEW.starts_at <= clock_timestamp() THEN RAISE EXCEPTION 'arrangement must start in future' USING ERRCODE = '23514'; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER outing_arrangement_guard BEFORE INSERT OR UPDATE OR DELETE ON outing_arrangements FOR EACH ROW EXECUTE FUNCTION outing_arrangement_guard();
-- +goose StatementBegin
CREATE FUNCTION outing_confirmation_invites() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO outing_invitations (outing_id, member_id, phase)
    SELECT NEW.outing_id, m.member_id, 'confirmation' FROM outings o JOIN group_members m ON m.group_id = o.group_id WHERE o.id = NEW.outing_id ORDER BY m.member_id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER outing_confirmation_invites AFTER INSERT ON outing_arrangements FOR EACH ROW EXECUTE FUNCTION outing_confirmation_invites();
-- +goose StatementBegin
CREATE FUNCTION outing_member_invites() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target BIGINT; start_time TIMESTAMPTZ; failure_constraint TEXT;
BEGIN
    PERFORM id FROM groups WHERE id = NEW.group_id FOR UPDATE;
    FOR target IN SELECT id FROM outings WHERE group_id = NEW.group_id ORDER BY id LOOP
        PERFORM outing_lock(target);
        SELECT starts_at INTO start_time FROM outing_arrangements WHERE outing_id = target;
        IF start_time IS NULL OR start_time > clock_timestamp() THEN
            BEGIN
                INSERT INTO outing_invitations (outing_id, member_id, phase)
                VALUES (target, NEW.member_id, CASE WHEN start_time IS NULL THEN 'decision' ELSE 'confirmation' END)
                ON CONFLICT DO NOTHING;
            EXCEPTION WHEN raise_exception THEN
                -- Crossing the cutoff during insertion skips this invitation,
                -- rather than rolling back an otherwise valid group join.
                GET STACKED DIAGNOSTICS failure_constraint = CONSTRAINT_NAME;
                IF failure_constraint <> 'outing_archived' THEN RAISE; END IF;
            END;
        END IF;
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER outing_member_invites AFTER INSERT ON group_members FOR EACH ROW EXECUTE FUNCTION outing_member_invites();

-- +goose StatementBegin
CREATE FUNCTION outing_membership_complete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM outings o JOIN outing_invitations i ON i.outing_id = o.id
        WHERE o.group_id = OLD.group_id AND i.member_id = OLD.member_id
        AND NOT EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = o.group_id AND m.member_id = i.member_id)
    ) THEN
        RAISE EXCEPTION 'invitation membership required' USING ERRCODE = '23514', CONSTRAINT = 'outing_invitation_member';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER outing_membership_complete AFTER UPDATE OR DELETE ON group_members DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION outing_membership_complete();

-- +goose Down
-- Destructive: removes outings and responses, preserving groups and accounts.
DROP TRIGGER outing_member_invites ON group_members;
DROP TRIGGER outing_membership_complete ON group_members;
DROP TABLE outing_availability, outing_member_inputs, outing_invitations, outing_arrangements, outing_dates, outings;
DROP FUNCTION outing_membership_complete(), outing_member_invites(), outing_confirmation_invites(), outing_arrangement_guard(), outing_input_complete(), outing_input_touch(), outing_input_guard(), outing_invitation_guard(), outing_dates_complete(), outing_dates_guard(), outings_guard(), outing_check_phase(BIGINT,TEXT), outing_lock(BIGINT);
