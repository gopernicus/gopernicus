-- @database: primary

-- @func: List
-- @filter:conditions event_type, published
-- @order: created_at
-- @max: 100
SELECT *
FROM event_outbox
WHERE $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM event_outbox
WHERE event_id = @event_id
;

-- @func: Create
-- @fields: event_id, event_type, payload
INSERT INTO event_outbox
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: published
UPDATE event_outbox
SET $fields
WHERE event_id = @event_id
RETURNING *;

-- @func: Delete
DELETE FROM event_outbox
WHERE event_id = @event_id
;
