-- @func: List
-- @filter:conditions *
-- @search: ilike(name, event_type, cron_expr)
-- @order: *
-- @max: 100
SELECT *
FROM job_schedules
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM job_schedules
WHERE schedule_id = @schedule_id
;

-- @func: GetByName
SELECT *
FROM job_schedules
WHERE name = @name
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO job_schedules
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-schedule_id,-created_at
UPDATE job_schedules
SET $fields
WHERE schedule_id = @schedule_id
RETURNING *;

-- @func: Delete
DELETE FROM job_schedules
WHERE schedule_id = @schedule_id
;
