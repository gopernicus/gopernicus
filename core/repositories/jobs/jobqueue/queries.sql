-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(event_type, correlation_id, tenant_id, aggregate_type, aggregate_id, status, worker_name, failure_reason)
-- @order: *
-- @max: 100
SELECT *
FROM job_queue
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM job_queue
WHERE job_id = @job_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO job_queue
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-job_id,-created_at
UPDATE job_queue
SET $fields
WHERE job_id = @job_id
RETURNING *;

-- @func: Delete
DELETE FROM job_queue
WHERE job_id = @job_id
;
