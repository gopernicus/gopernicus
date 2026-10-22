-- Challenge subject keys (coordination-hub-auth-upstream CHAU-6.1). APPEND-ONLY:
-- 0011_challenges.sql is already tagged and immutable, so the generalization
-- arrives as ALTERs here. Every existing challenge row is PRESERVED.
--
-- Why: a live challenge was unique under (user_id, purpose), which assumes every
-- challenge HAS a user. An email magic link may now be issued for an address with
-- no account at all (provision-on-consumption), so there is no user id to key on.
-- Putting a digest into user_id would make that column mean two different things
-- depending on purpose — the exact kind of overloading that turns into a
-- half-remembered rule. subject_key is the honest name for "the thing this
-- challenge is unique under".
--
-- It is a PII-FREE digest, never a raw address: the passwordless issuer derives it
-- from the host IdentifierKeyer plus the purpose, the same key material the rate
-- limiter and the delivery logical key already use.
--
-- Every pre-existing purpose keys on the user id, so the backfill is exactly
-- subject_key := user_id and their behavior is unchanged.
ALTER TABLE challenges
    ADD COLUMN IF NOT EXISTS subject_key TEXT NOT NULL DEFAULT '';

UPDATE challenges SET subject_key = user_id WHERE subject_key = '';

-- The uniqueness moves from (user_id, purpose) to (subject_key, purpose). Dropping
-- the old index is required, not cosmetic: leaving it would forbid two magic-link
-- challenges that share an empty user_id for different addresses.
DROP INDEX IF EXISTS idx_challenges_user_purpose;

CREATE UNIQUE INDEX IF NOT EXISTS idx_challenges_subject_purpose
    ON challenges (subject_key, purpose);
