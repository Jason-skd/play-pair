-- +goose Up
CREATE TABLE groups (
    id BIGINT GENERATED ALWAYS AS IDENTITY CONSTRAINT groups_pkey PRIMARY KEY,
    name TEXT NOT NULL,
    join_code TEXT NOT NULL,
    initiator_id BIGINT NOT NULL,
    creation_key UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT groups_id_positive CHECK (id > 0),
    CONSTRAINT groups_name_check CHECK (char_length(name) BETWEEN 1 AND 80 AND name = btrim(name, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000')),
    CONSTRAINT groups_join_code_check CHECK (join_code ~ '^[A-Z2-7]{16}$'),
    CONSTRAINT groups_join_code_key UNIQUE (join_code),
    CONSTRAINT groups_creation_key UNIQUE (initiator_id, creation_key)
);
CREATE TABLE group_members (
    group_id BIGINT NOT NULL,
    member_id BIGINT NOT NULL,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT group_members_pkey PRIMARY KEY (group_id, member_id),
    CONSTRAINT group_members_group_fkey FOREIGN KEY (group_id) REFERENCES groups(id),
    CONSTRAINT group_members_user_fkey FOREIGN KEY (member_id) REFERENCES users(id)
);
ALTER TABLE groups ADD CONSTRAINT groups_initiator_member_fkey
    FOREIGN KEY (id, initiator_id) REFERENCES group_members(group_id, member_id)
    ON DELETE NO ACTION ON UPDATE NO ACTION DEFERRABLE INITIALLY DEFERRED;

-- +goose StatementBegin
CREATE FUNCTION groups_stamp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        NEW.created_at := clock_timestamp();
        NEW.updated_at := NEW.created_at;
    ELSIF NEW IS DISTINCT FROM OLD THEN
        NEW.updated_at := greatest(OLD.updated_at, clock_timestamp());
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER groups_stamp BEFORE INSERT OR UPDATE ON groups
    FOR EACH ROW EXECUTE FUNCTION groups_stamp();

-- +goose StatementBegin
CREATE FUNCTION group_members_touch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target_id BIGINT;
    targets BIGINT[];
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NULL; END IF;
        targets := ARRAY[OLD.group_id, NEW.group_id];
    ELSIF TG_OP = 'DELETE' THEN
        targets := ARRAY[OLD.group_id];
    ELSE
        targets := ARRAY[NEW.group_id];
    END IF;
    FOR target_id IN SELECT DISTINCT unnest(targets) ORDER BY 1 LOOP
        UPDATE groups SET updated_at = greatest(updated_at, clock_timestamp()) WHERE id = target_id;
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER group_members_touch AFTER INSERT OR UPDATE OR DELETE ON group_members
    FOR EACH ROW EXECUTE FUNCTION group_members_touch();

-- +goose Down
-- Destructive: removes all group and membership data. Existing accounts remain.
ALTER TABLE groups DROP CONSTRAINT groups_initiator_member_fkey;
DROP TABLE group_members;
DROP TABLE groups;
DROP FUNCTION group_members_touch();
DROP FUNCTION groups_stamp();
