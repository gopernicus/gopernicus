-- Machine identities (design §4.1). act_as_user marks a personal account whose
-- keys resolve to the human owner_user_id; the ActAsUser → owner_user_id != ''
-- invariant is enforced in the domain, not here. act_as_user is 0/1;
-- created_at/updated_at are fixed-width TEXT timestamps. List is keyset-paginated
-- created_at DESC, id DESC. created_by/owner_user_id reference users.id by
-- convention (no enforced FK).
CREATE TABLE IF NOT EXISTS service_accounts (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL,
    act_as_user   INTEGER NOT NULL DEFAULT 0,
    owner_user_id TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
