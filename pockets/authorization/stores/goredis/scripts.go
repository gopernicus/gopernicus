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
  if m[1] ~= '2' or not m[2] or m[2] == '' or not m[3] or m[3] == '' or
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
redis.call('HSET', KEYS[2], '!schema', '2', '!binding', ARGV[3], '!receipt', ARGV[4], '!until', ARGV[5])
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
local function valid_tuple(t)
  if not array(t) or #t ~= 7 or (t[1] ~= '1' and t[1] ~= '2') then return false end
  if t[1] == '1' and (t[2] ~= '' or t[3] ~= '') then return false end
  for i = 2, 7 do
    if type(t[i]) ~= 'string' or #t[i] > 342 or not encoded(t[i], i == 4 or i == 5 or i == 6 or (t[1] == '2' and i <= 3)) then return false end
  end
  return true
end
local function fields(t)
  return 'f:' .. cjson.encode({t[1], t[2], t[3], t[4]}), 'r:' .. cjson.encode({t[5], t[6], t[7]})
end
local operations = cjson.decode(ARGV[6])
if not array(operations) then return -1 end
local sets, order = {}, {}
local function load_set(field)
  if sets[field] then return sets[field] end
  local value = redis.call('HGET', KEYS[1], field)
  local facts = {}
  if value then
    if value:sub(1, 1) ~= '[' then error('invalid tuple set') end
    local decoded = cjson.decode(value)
    if not array(decoded) then error('invalid tuple set') end
    for _, fact in ipairs(decoded) do
      if not valid_tuple(fact) then error('invalid canonical tuple') end
      local f, r = fields(fact)
      if field ~= f and field ~= r then error('misplaced canonical tuple') end
      local identity = cjson.encode(fact)
      if facts[identity] then error('duplicate canonical tuple') end
      facts[identity] = fact
    end
  end
  sets[field] = facts
  order[#order+1] = field
  return facts
end
-- Validate and transform all affected fields before changing Redis: Lua errors
-- after writes are not rolled back by Redis.
for _, op in ipairs(operations) do
  if type(op) ~= 'table' or type(op.remove) ~= 'boolean' or not valid_tuple(op.tuple) then return -1 end
  local forward, reverse = fields(op.tuple)
  if op.forward ~= forward or op.reverse ~= reverse or not allowed[forward] or not allowed[reverse] then return -1 end
  local f, r = load_set(forward), load_set(reverse)
  local identity = cjson.encode(op.tuple)
  if op.remove then f[identity], r[identity] = nil, nil
  else f[identity], r[identity] = op.tuple, op.tuple end
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
redis.call('HSET', KEYS[1], '!schema', '2', '!binding', ARGV[3], '!receipt', ARGV[4], '!until', ARGV[5])
return 1
`)
)
