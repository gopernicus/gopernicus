import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";

const script = readFileSync(0, "utf8");
const origin = "https://app.example";
const defaultConfig = {
  checkUrl: "/tenant/auth/me",
  refreshUrl: "/tenant/auth/refresh",
  returnTo: "/dashboard",
  lockName: "gopernicus:session-refresh",
};

function locks() {
  let queue = Promise.resolve();
  let active = false;
  return {
    request(name, callback) {
      assert.equal(name, "gopernicus:session-refresh");
      const task = queue.then(async () => {
        assert.equal(active, false, "refresh flows must never overlap");
        active = true;
        try { return await callback(); } finally { active = false; }
      });
      queue = task.catch(() => {});
      return task;
    },
  };
}

function tab(options = {}) {
  const config = { ...defaultConfig, ...options.config };
  const requests = [];
  const navigations = [];
  const listeners = new Map();
  const storage = options.storage ?? new Map();
  const status = { hidden: true, textContent: "" };
  const result = {
    requests, navigations, storage, status,
    interact(event = "input") { listeners.get(event)?.(); },
  };
  let responseIndex = 0;
  const document = {
    currentScript: { dataset: config, previousElementSibling: status },
    addEventListener(event, callback) { listeners.set(event, callback); },
    removeEventListener(event) { listeners.delete(event); },
    get cookie() { throw new Error("recovery must not read cookies"); },
    set cookie(_) { assert.fail("recovery must not clear or write cookies"); },
  };
  const context = {
    document, URL,
    navigator: { locks: options.noLocks ? undefined : options.locks ?? locks() },
    location: {
      origin, href: origin + "/tenant/auth/login?recover=1",
      replace(url) { navigations.push(url); },
    },
    Date: { now: () => typeof options.now === "function" ? options.now() : options.now ?? 100000 },
    get sessionStorage() {
      if (options.storageUnavailable) throw new Error("storage denied");
      return {
        getItem(key) { return storage.get(key) ?? null; },
        setItem(key, value) {
          if (options.storageReadOnly) throw new Error("storage read-only");
          if (!options.storageSilentFailure) storage.set(key, value);
        },
      };
    },
    async fetch(url, init) {
      assert.equal(init.credentials, "same-origin");
      assert.equal(init.cache, "no-store");
      assert.equal(init.redirect, "error");
      assert.equal(new URL(url).origin, origin);
      requests.push({ url, method: init.method });
      const next = options.fetch
        ? await options.fetch(url, init)
        : (options.responses ?? [200])[responseIndex++];
      options.onFetch?.(result, requests.length);
      if (next instanceof Error) throw next;
      assert.equal(typeof next, "number", "unexpected extra request");
      return {
        status: next, ok: next >= 200 && next < 300,
        json() { assert.fail("must not read a token response body"); },
        text() { assert.fail("must not read a token response body"); },
      };
    },
  };
  result.done = runInNewContext(script, context);
  return result;
}

function expectCalls(result, methods) {
  assert.deepEqual(result.requests.map(({ method }) => method), methods);
  for (const { method, url } of result.requests) {
    assert.equal(url, origin + (method === "POST" ? defaultConfig.refreshUrl : defaultConfig.checkUrl));
  }
}

for (const responses of [[200], [204], [401, 200, 200], [401, 204, 204]]) {
  const result = tab({ responses });
  await result.done;
  expectCalls(result, responses.length === 1 ? ["GET"] : ["GET", "POST", "GET"]);
  assert.deepEqual(result.navigations, [origin + "/dashboard"]);
  assert.equal(result.storage.size, 1, "successful attempts retain their cooldown");
}

for (const responses of [
  [403], [429], [500], [302], [new Error("offline")],
  [401, 401], [401, 403], [401, 429], [401, 500], [401, new Error("offline")],
  [401, 200, 401], [401, 200, 403], [401, 200, 500], [401, 200, new Error("offline")],
]) {
  const result = tab({ responses });
  await result.done;
  assert.equal(result.requests.length, responses.length);
  assert.deepEqual(result.navigations, []);
  assert.match(result.status.textContent, /Please sign in/);
}

// Four expired tabs share one rotation; later lock holders recheck fresh cookies.
let valid = false;
let refreshes = 0;
const sharedLocks = locks();
const tabs = Array.from({ length: 4 }, () => tab({
  locks: sharedLocks,
  async fetch(_, { method }) {
    if (method === "POST") {
      refreshes++;
      valid = true;
      return 200;
    }
    return valid ? 200 : 401;
  },
}));
await Promise.all(tabs.map(({ done }) => done));
assert.equal(refreshes, 1);
for (const result of tabs) assert.deepEqual(result.navigations, [origin + "/dashboard"]);

for (const options of [
  { noLocks: true }, { storageUnavailable: true }, { storageReadOnly: true },
  { storageSilentFailure: true },
  { config: { checkUrl: "https://other.example/auth/me" } },
  { config: { refreshUrl: "//other.example/auth/refresh" } },
  { config: { checkUrl: "https://user:password@app.example/auth/me" } },
  { config: { returnTo: "javascript:alert(1)" } },
]) {
  const result = tab(options);
  await result.done;
  assert.deepEqual(result.requests, []);
  assert.deepEqual(result.navigations, []);
  assert.equal(result.status.hidden, true);
}

const storage = new Map();
await tab({ storage }).done;
const repeated = tab({ storage });
await repeated.done;
expectCalls(repeated, []);
const later = tab({ storage, now: 130001 });
await later.done;
expectCalls(later, ["GET"]);
const differentTarget = tab({ storage, config: { returnTo: "/another-page" } });
await differentTarget.done;
expectCalls(differentTarget, ["GET"]);
const differentMount = tab({ storage, config: { checkUrl: "/other/auth/me" } });
await differentMount.done;
assert.equal(differentMount.requests.length, 1);

// A slow lock wait and network response still leave a fresh receipt on return.
let clock = 100000;
const slowStorage = new Map();
const slowLocks = locks();
const slow = tab({
  storage: slowStorage,
  now: () => clock,
  locks: {
    request(name, callback) {
      assert.deepEqual([...slowStorage.values()], ["100000"]);
      clock += 60000;
      return slowLocks.request(name, callback);
    },
  },
  fetch() { clock += 60000; return 200; },
});
await slow.done;
assert.deepEqual(slow.navigations, [origin + "/dashboard"]);
assert.deepEqual([...slowStorage.values()], [String(clock)]);
const immediatelyReturned = tab({ storage: slowStorage, now: clock });
await immediatelyReturned.done;
expectCalls(immediatelyReturned, []);

const externalReturn = tab({ config: { returnTo: "https://allowed.example/return" } });
await externalReturn.done;
assert.deepEqual(externalReturn.navigations, ["https://allowed.example/return"]);

for (const event of ["input", "submit", "pointerdown", "keydown"]) {
  const queued = tab();
  queued.interact(event);
  await queued.done;
  expectCalls(queued, []);
  assert.deepEqual(queued.navigations, []);
  for (const cancelAt of [1, 2, 3]) {
    const result = tab({
      responses: [401, 200, 200],
      onFetch(result, count) { if (count === cancelAt) result.interact(event); },
    });
    await result.done;
    assert.equal(result.requests.length, cancelAt);
    assert.deepEqual(result.navigations, []);
    assert.equal(result.status.hidden, true);
  }
}
