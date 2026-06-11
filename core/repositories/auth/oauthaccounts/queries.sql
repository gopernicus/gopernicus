-- @func: List
-- @filter:conditions *
-- @search: ilike(provider, provider_user_id, provider_email, scope)
-- @order: *
-- @max: 100
SELECT *
FROM oauth_accounts
WHERE parent_user_id = @parent_user_id AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM oauth_accounts
WHERE oauth_account_id = @oauth_account_id AND parent_user_id = @parent_user_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO oauth_accounts
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-oauth_account_id,-parent_user_id,-created_at
UPDATE oauth_accounts
SET $fields
WHERE oauth_account_id = @oauth_account_id AND parent_user_id = @parent_user_id
RETURNING *;

-- @func: Delete
DELETE FROM oauth_accounts
WHERE oauth_account_id = @oauth_account_id AND parent_user_id = @parent_user_id
;

-- GetByProvider, ListByUser, and DeleteByUserAndProvider back the emitted
-- authentication satisfier (satisfiers/oauth_accounts.go) — its repo
-- interface requires all three.

-- @func: GetByProvider
SELECT *
FROM oauth_accounts
WHERE provider = @provider AND provider_user_id = @provider_user_id
;

-- @func: ListByUser
-- @scan: many
-- @type:limit int
SELECT *
FROM oauth_accounts
WHERE parent_user_id = @parent_user_id
ORDER BY linked_at DESC
LIMIT @limit
;

-- @func: DeleteByUserAndProvider
DELETE FROM oauth_accounts
WHERE parent_user_id = @parent_user_id AND provider = @provider
;

