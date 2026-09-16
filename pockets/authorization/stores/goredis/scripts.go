package goredis

import "github.com/redis/go-redis/v9"

const metadataLua = `
local function now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end
local function state(key)
  local typ = redis.call('TYPE', key).ok
  if typ == 'none' then return '', '', 0 end
  if typ ~= 'hash' then error('tuple mirror has incorrect Redis type') end
  local m = redis.call('HMGET', key, '!schema', '!binding', '!receipt', '!until')
  local until_ms = tonumber(m[4])
  if m[1] ~= '1' or not m[2] or m[2] == '' or not m[3] or m[3] == '' or
    not until_ms or until_ms <= 0 or until_ms > 9007199254740991 or until_ms ~= math.floor(until_ms) then
    return '', '', 0
  end
  return m[2], m[3], until_ms
end
local function matches(key, binding, receipt)
  local b, r = state(key)
  return b == binding and r == receipt
end
`

var (
	stateScript = redis.NewScript(metadataLua + `
local binding, receipt = state(KEYS[1])
return {binding, receipt}
`)
	readScript = redis.NewScript(metadataLua + `
local binding, receipt, until_ms = state(KEYS[1])
local remaining = until_ms - now_ms()
if binding == '' or binding ~= ARGV[1] or receipt ~= ARGV[2] or remaining <= 0 then return {0} end
local bytes = 0
for i = 4, #ARGV do
  local size = redis.call('HSTRLEN', KEYS[1], ARGV[i])
  bytes = bytes + math.max(size, 2)
  if bytes > tonumber(ARGV[3]) then return {-2} end
end
local result = {remaining}
for i = 4, #ARGV do
  result[#result+1] = redis.call('HGET', KEYS[1], ARGV[i]) or '[]'
end
return result
`)
	prepareScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) ~= 0 then return redis.error_reply('temporary tuple mirror already exists') end
redis.call('HSET', KEYS[1], '!prepared', '1')
redis.call('PEXPIRE', KEYS[1], ARGV[1])
return 1
`)
	buildScript = redis.NewScript(`
if redis.call('TYPE', KEYS[1]).ok ~= 'hash' or
  redis.call('HGET', KEYS[1], '!prepared') ~= '1' or redis.call('PTTL', KEYS[1]) <= 0 then
  return redis.error_reply('temporary tuple mirror was lost or expired')
end
redis.call('HSET', KEYS[1], unpack(ARGV))
return 1
`)
	replaceScript = redis.NewScript(metadataLua + `
if not matches(KEYS[1], ARGV[1], ARGV[2]) then return 0 end
local deadline = tonumber(ARGV[5])
if not deadline or deadline <= now_ms() then return -1 end
if redis.call('TYPE', KEYS[2]).ok ~= 'hash' or redis.call('HGET', KEYS[2], '!prepared') ~= '1' then return -1 end
redis.call('HSET', KEYS[2], '!schema', '1', '!binding', ARGV[3], '!receipt', ARGV[4], '!until', ARGV[5])
redis.call('HDEL', KEYS[2], '!prepared')
redis.call('RENAME', KEYS[2], KEYS[1])
redis.call('PERSIST', KEYS[1])
return 1
`)
	deltaScript = redis.NewScript(metadataLua + `
if not matches(KEYS[1], ARGV[1], ARGV[2]) then return 0 end
local binding = state(KEYS[1])
if binding == '' then return -1 end
local deadline = tonumber(ARGV[5])
if not deadline or deadline <= now_ms() then return -1 end
-- Bound incoming operations and distinct existing fields before decoding either.
-- Field names are passed separately so no JSON decode is needed for this check.
local max_bytes, bytes, allowed = tonumber(ARGV[7]), #ARGV[6], {}
if bytes > max_bytes then return -2 end
for i = 8, #ARGV do
  if not allowed[ARGV[i]] then
    bytes = bytes + redis.call('HSTRLEN', KEYS[1], ARGV[i])
    if bytes > max_bytes then return -2 end
    allowed[ARGV[i]] = true
  end
end
local function encoded(s, required)
  if type(s) ~= 'string' or (required and s == '') then return false end
  if s:find('[^A-Za-z0-9_%-]') then return false end
  local mod = #s % 4
  if mod == 1 then return false end
  if mod == 2 and not s:sub(-1):match('[AQgw]') then return false end
  if mod == 3 and not s:sub(-1):match('[AEIMQUYcgkosw048]') then return false end
  return true
end
local function array(a)
  if type(a) ~= 'table' then return false end
  for k, _ in pairs(a) do
    if type(k) ~= 'number' or k < 1 or k > #a or k ~= math.floor(k) then return false end
  end
  return true
end
local function valid_ref(ref, resource)
  return array(ref) and #ref == 3 and encoded(ref[1], true) and encoded(ref[2], true) and encoded(ref[3], resource)
end
local operations = cjson.decode(ARGV[6])
if not array(operations) then return -1 end
local sets, order = {}, {}
local function load_set(field, resource)
  if sets[field] then return sets[field] end
  local value = redis.call('HGET', KEYS[1], field)
  local refs = {}
  if value then
    if value:sub(1, 1) ~= '[' then error('invalid tuple set') end
    local decoded = cjson.decode(value)
    if not array(decoded) then error('invalid tuple set') end
    for _, ref in ipairs(decoded) do
      if not valid_ref(ref, resource) then error('invalid tuple reference') end
      local identity = cjson.encode(ref)
      if refs[identity] then error('duplicate tuple reference') end
      refs[identity] = ref
    end
  end
  sets[field] = refs
  order[#order+1] = field
  return refs
end
-- Decode, validate, and transform all affected fields before changing Redis.
-- Redis does not roll back a script that errors after its first write.
for _, op in ipairs(operations) do
  if type(op) ~= 'table' or type(op.remove) ~= 'boolean' or
    not valid_ref(op.resource, true) or not valid_ref(op.subject, false) then return -1 end
  local forward = 'f:' .. cjson.encode(op.resource)
  local reverse = 'r:' .. cjson.encode(op.subject)
  if op.forward ~= forward or op.reverse ~= reverse or not allowed[forward] or not allowed[reverse] then return -1 end
  local f = load_set(forward, false)
  local r = load_set(reverse, true)
  local sk, rk = cjson.encode(op.subject), cjson.encode(op.resource)
  if op.remove then f[sk], r[rk] = nil, nil
  else f[sk], r[rk] = op.subject, op.resource end
end
local values, deleted = {}, {}
for _, field in ipairs(order) do
  local refs = {}
  for _, ref in pairs(sets[field]) do refs[#refs+1] = ref end
  if #refs == 0 then deleted[#deleted+1] = field
  else values[#values+1] = {field, cjson.encode(refs)} end
end
if deadline <= now_ms() then return -1 end
-- A memory/command failure during writes must leave an unavailable mirror,
-- never changed indexes under the previously complete receipt.
redis.call('HDEL', KEYS[1], '!schema')
for _, pair in ipairs(values) do redis.call('HSET', KEYS[1], pair[1], pair[2]) end
for _, field in ipairs(deleted) do redis.call('HDEL', KEYS[1], field) end
redis.call('HSET', KEYS[1], '!schema', '1', '!binding', ARGV[3], '!receipt', ARGV[4], '!until', ARGV[5])
return 1
`)
)
