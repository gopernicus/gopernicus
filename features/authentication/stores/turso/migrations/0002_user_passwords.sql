-- Credential material, kept out of the users table so a store can guard it
-- independently. One row per user (user_id PRIMARY KEY); Set is an upsert on the
-- primary key. user_id references users.id by convention — the port keys the row
-- by a bare user id (no enforced FK), matching the PasswordRepository contract.
CREATE TABLE IF NOT EXISTS user_passwords (
    user_id TEXT PRIMARY KEY,
    hash    TEXT NOT NULL
);
