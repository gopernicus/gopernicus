#!/usr/bin/env python3
"""Owned local authorization fixtures. No existing endpoints or cloud targets.

The baseline mode runs SQL adapter tests and two-process direct baselines.
The sql mode adds LRU decision caching; redis adds LRU and shared Redis legs.
Mounted runs combined authentication/authorization HTTP regressions over SQL.
Firestore runs an owned digest-pinned emulator and LRU/Redis two-process legs.
All runs the SQL and emulator cache legs plus mounted HTTP regressions.
Benchmark records local two-process fixed-arrival latency and provisional thresholds.
"""
import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import selectors
import signal
import shutil
import socket
import subprocess
import tempfile
import time
import threading
from concurrent.futures import ThreadPoolExecutor
import uuid

MODULES = ("sdk", "pockets", "pockets/authorization",
           "pockets/authorization/stores/pgx", "integrations/datastores/pgxdb",
           "pockets/authorization/stores/turso", "integrations/datastores/turso",
           "integrations/kvstores/goredis", "integrations/cryptids/bcrypt",
           "pockets/authentication", "pockets/authentication/stores/pgx",
           "pockets/authentication/stores/turso", "integrations/datastores/firestore",
           "pockets/authorization/stores/firestore")


def clean_environment(parent, root):
    # Allowlist rather than stripping known database/provider variable names.
    env = {key: parent[key] for key in ("PATH", "HOME", "TMPDIR", "SYSTEMROOT") if key in parent}
    env.update(GOWORK="off", GOENV="off", GOPROXY="off", GOTOOLCHAIN="local",
               GOCACHE=str(root / "go-cache"), LC_ALL="C", LANG="C")
    return env


def live_environment(parent):
    if parent.get("FIRESTORE_EMULATOR_HOST"):
        raise ValueError("firestore-live refuses an emulator endpoint")
    project = parent.get("FIRESTORE_LIVE_PROJECT_ID", "")
    database = parent.get("FIRESTORE_LIVE_DATABASE_ID", "")
    owned = parent.get("FIRESTORE_LIVE_OWNED", "")
    credential = parent.get("GOOGLE_APPLICATION_CREDENTIALS", "")
    if not re.fullmatch(r"[a-z][a-z0-9-]{4,28}[a-z0-9]", project):
        raise ValueError("firestore-live requires explicit FIRESTORE_LIVE_PROJECT_ID")
    if not re.fullmatch(r"(?:ci-|authorization-cache-)[a-z0-9-]+", database) or database != owned:
        raise ValueError("firestore-live requires a disposable ci-/authorization-cache- database and matching FIRESTORE_LIVE_OWNED; default/unowned targets refused")
    if not credential or not Path(credential).is_absolute() or not Path(credential).is_file():
        raise ValueError("firestore-live requires an explicit GOOGLE_APPLICATION_CREDENTIALS file path; credential contents are never recorded")
    return {"FIRESTORE_LIVE_PROJECT_ID": project, "FIRESTORE_LIVE_DATABASE_ID": database,
            "FIRESTORE_LIVE_OWNED": owned, "GOOGLE_APPLICATION_CREDENTIALS": credential,
            "FIRESTORE_LIVE_REQUIRED": "1"}


class OwnedFixture:
    def __init__(self):
        self.root = Path(tempfile.mkdtemp(prefix="authorization-cache-" )).resolve()
        self.token = uuid.uuid4().hex
        (self.root / "owner").write_text(self.token)
        self.env = clean_environment(os.environ, self.root)
        self.processes = []
        self.logs = []
        self.endpoints = {}
        self.events = []
        self.sequence = 0
        self.control_lock = threading.Lock()
        self.container_files = []

    def run(self, argv, cwd=None, timeout=180):
        process = subprocess.Popen([str(x) for x in argv], cwd=cwd or self.root,
                                   env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, start_new_session=True)
        try:
            stdout, stderr = process.communicate(timeout=timeout)
        except BaseException:
            # The new session belongs solely to this command, including go test children.
            # Kill the group even if its leader already exited with inherited pipes open.
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.communicate()
            raise
        result = subprocess.CompletedProcess(argv, process.returncode, stdout, stderr)
        self.events.append({"command": [str(x) for x in argv], "returncode": result.returncode,
                            "stdout": result.stdout, "stderr": result.stderr})
        if result.returncode:
            details = result.stderr or result.stdout
            if result.stdout.startswith('{'):
                lines = []
                for line in result.stdout.splitlines():
                    try:
                        event = json.loads(line)
                    except ValueError:
                        continue
                    if ".go:" in event.get("Output", ""):
                        lines.append(event["Output"].strip())
                if lines:
                    details = " | ".join(lines)
            raise RuntimeError(f"command failed: {argv}: {details[-4000:]}")
        return result.stdout

    def start(self, argv, name, control=False):
        log = open(self.root / (name + ".log"), "w")
        self.logs.append(log)
        process = subprocess.Popen([str(x) for x in argv], cwd=self.root, env=self.env,
                                   stdin=subprocess.PIPE if control else subprocess.DEVNULL,
                                   stdout=subprocess.PIPE if control else log, stderr=log,
                                   text=True, bufsize=1)
        self.processes.append(process)
        self.events.append({"process": name, "pid": process.pid, "command": [str(x) for x in argv]})
        return process

    def port(self, name):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        self.endpoints[name] = ("127.0.0.1", port)
        return port

    def endpoint(self, name, host, port):
        if host != "127.0.0.1" or self.endpoints.get(name) != (host, port):
            raise ValueError("refusing unowned or non-loopback endpoint")
        return host, port

    def wait_ready(self, name, process, probe, timeout=20):
        host, port = self.endpoint(name, *self.endpoints[name])
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError(f"owned {name} exited: " + (self.root / (name + ".log")).read_text())
            try:
                with socket.create_connection((host, port), timeout=.2) as sock:
                    if probe:
                        sock.sendall(probe)
                        if sock.recv(64) != b"+PONG\r\n":
                            raise RuntimeError("unexpected Redis identity response")
                    return
            except (OSError, ConnectionError):
                time.sleep(.05)
        raise TimeoutError(f"owned {name} readiness timed out")

    def control(self, process, op, timeout=15, workload=""):
        if process not in self.processes or process.poll() is not None:
            raise ValueError("control requires a live owned process")
        started = time.perf_counter_ns()
        with self.control_lock:
            self.sequence += 1
            request_id = self.sequence
        process.stdin.write(json.dumps({"ID": request_id, "Op": op, "Workload": workload}) + "\n")
        process.stdin.flush()
        deadline = time.monotonic() + timeout
        line = bytearray()
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            while not line.endswith(b"\n"):
                remaining = deadline - time.monotonic()
                if remaining <= 0 or not selector.select(remaining):
                    raise TimeoutError(f"control {op} timed out")
                chunk = os.read(process.stdout.fileno(), 1)
                if not chunk or len(line) >= 8192:
                    raise RuntimeError("closed or oversized control response")
                line.extend(chunk)
        result = json.loads(line)
        if result["ID"] != request_id or result["PID"] != process.pid or result.get("Error"):
            raise RuntimeError(f"invalid control response: {result}")
        self.events.append({"control": op, "result": result, "elapsed_ns": time.perf_counter_ns() - started})
        return result

    def start_emulator(self):
        port = self.port("firestore")
        cidfile = self.root / "firestore.cid"
        self.container_files.append(cidfile)
        image = "gcr.io/google.com/cloudsdktool/google-cloud-cli:emulators@sha256:45a15cc163d2df13137478856b4b9bb90426100fa8aec542269460c10110b727"
        identity = self.run(["docker", "create", "--pull=never", "--cidfile", cidfile,
                             "--label", "authorization-cache-owner=" + self.token,
                             "--publish", f"127.0.0.1:{port}:8080", image,
                             "gcloud", "emulators", "firestore", "start",
                             "--host-port=0.0.0.0:8080", "--project=cache-" + self.token[:16]]).strip()
        self.run(["docker", "start", identity])
        info = json.loads(self.run(["docker", "inspect", identity]))[0]
        if info["Config"]["Labels"].get("authorization-cache-owner") != self.token or info["HostConfig"]["PortBindings"] != {"8080/tcp": [{"HostIp": "127.0.0.1", "HostPort": str(port)}]}:
            raise RuntimeError("emulator ownership/binding mismatch")
        deadline = time.monotonic() + 45
        host, port = self.endpoint("firestore", "127.0.0.1", port)
        while time.monotonic() < deadline:
            try:
                with socket.create_connection((host, port), timeout=.2):
                    break
            except OSError:
                time.sleep(.1)
        else:
            raise TimeoutError("owned emulator not ready")
        self.env["FIRESTORE_EMULATOR_HOST"] = f"{host}:{port}"
        self.env["FIRESTORE_PROJECT_ID"] = "cache-" + self.token[:16]
        self.env["FIXTURE_FIRESTORE_DATABASE"] = "cache-app"
        self.events.append({"emulator": identity, "image": image, "host": host, "port": port})

    def cleanup(self):
        # Only Popen handles created here are eligible; never kill by port/name.
        for process in reversed(self.processes):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=10)
            for stream in (process.stdin, process.stdout):
                if stream:
                    stream.close()
        for cidfile in self.container_files:
            if not cidfile.exists():
                continue
            identity = cidfile.read_text().strip()
            if len(identity) != 64 or any(c not in "0123456789abcdef" for c in identity):
                raise RuntimeError("invalid owned container id")
            info = json.loads(self.run(["docker", "inspect", identity]))[0]
            if info["Config"]["Labels"].get("authorization-cache-owner") != self.token:
                raise RuntimeError("container ownership mismatch; refusing removal")
            self.run(["docker", "rm", "--force", identity])
        for log in self.logs:
            log.close()
        if (self.root / "owner").read_text() != self.token:
            raise RuntimeError("ownership marker mismatch: preserving directory")
        shutil.rmtree(self.root)


def generate(fixture, source):
    """Snapshot local modules so concurrent repository changes cannot alter a run."""
    pinned = fixture.root / "source"
    digest = hashlib.sha256()
    for module in MODULES:
        original = source / module
        destination = pinned / module
        destination.mkdir(parents=True, exist_ok=True)
        for path in sorted(original.rglob("*")):
            if not path.is_file() or path.is_symlink() or any(part.startswith(".") for part in path.relative_to(original).parts):
                continue
            relative = path.relative_to(original)
            # Each nested module is copied by its own entry only.
            if any((original / parent / "go.mod").exists() for parent in relative.parents if parent != Path(".")):
                continue
            target = destination / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            data = path.read_bytes()
            target.write_bytes(data)
            digest.update(str(path.relative_to(source)).encode() + b"\0" + data)
    host = fixture.root / "host"
    host.mkdir()
    replacements = "\n".join(f'replace github.com/gopernicus/gopernicus/{module} => "{pinned / module}"' for module in MODULES)
    for template in (source / ".github/scripts/authorization_cache_fixture").glob("*.tmpl"):
        (host / template.name.removesuffix(".tmpl")).write_text(template.read_text().replace("@REPLACEMENTS@", replacements))
    fixture.events.append({"source_sha256": digest.hexdigest(), "modules": list(MODULES)})
    return host


def sql_baseline(fixture, source, postgres_bin, mode="baseline"):
    host = generate(fixture, source)
    fixture.run(["go", "mod", "tidy"], cwd=host)
    fixture.run(["go", "build", "-o", str(fixture.root / "app"), "."], cwd=host)
    fixture.run(["go", "test", "-run", "^TestFixtureModel$", "./..."], cwd=host)
    fixture.run(["go", "vet", "./..."], cwd=host)
    pg = Path(postgres_bin)
    version = fixture.run([pg / "postgres", "--version"])
    if " 17." not in version:
        raise ValueError("fixture requires PostgreSQL 17")
    data = fixture.root / "pgdata"
    fixture.run([pg / "initdb", "-D", data, "-U", "fixture", "-A", "trust", "--encoding=UTF8", "--locale=C"])
    port = fixture.port("postgres")
    process = fixture.start([pg / "postgres", "-D", data, "-h", "127.0.0.1", "-p", port, "-k", str(fixture.root)], "postgres")
    fixture.wait_ready("postgres", process, None)
    identity = fixture.run([pg / "psql", "-h", "127.0.0.1", "-p", port, "-U", "fixture", "-d", "postgres", "-Atc", "SHOW data_directory"]).strip()
    if Path(identity).resolve() != data.resolve() or process.poll() is not None:
        raise RuntimeError("PostgreSQL ownership identity mismatch")
    fixture.run([pg / "createdb", "-h", "127.0.0.1", "-p", port, "-U", "fixture", "authorization_fixture"])
    fixture.env["FIXTURE_DSN"] = f"postgres://fixture@127.0.0.1:{port}/authorization_fixture?sslmode=disable"
    redis_port = fixture.port("redis")
    redis = fixture.start(["redis-server", "--bind", "127.0.0.1", "--port", redis_port, "--save", "", "--appendonly", "no", "--dir", fixture.root], "redis")
    fixture.wait_ready("redis", redis, b"*1\r\n$4\r\nPING\r\n")
    with socket.create_connection(fixture.endpoint("redis", "127.0.0.1", redis_port), timeout=2) as sock:
        sock.sendall(b"*2\r\n$4\r\nINFO\r\n$6\r\nserver\r\n")
        info = sock.recv(8192).decode()
        if f"process_id:{redis.pid}\r\n" not in info or redis.poll() is not None:
            raise RuntimeError("Redis ownership identity mismatch")
    fixture.env["FIXTURE_REDIS"] = f"127.0.0.1:{redis_port}"
    fixture.env["POSTGRES_TEST_DSN"] = fixture.env["FIXTURE_DSN"]
    run_adapter_cache_tests(fixture)
    mounted_errors = []
    fixture.env["FIXTURE_BACKEND"] = "pgx"
    direct_checks(fixture)
    if mode == "benchmark":
        benchmark_backend(fixture)
    elif mode == "mounted":
        try:
            run_mounted_tests(fixture)
        except RuntimeError as exc:
            mounted_errors.append(fixture.env["FIXTURE_BACKEND"] + ": " + str(exc))
    elif mode not in ("baseline", "firestore"):
        cached_checks(fixture, "redis" if mode == "all" else mode)
    if mode == "all":
        run_mounted_tests(fixture)
    fixture.env["FIXTURE_BACKEND"] = "turso"
    fixture.env["FIXTURE_DSN"] = "file:" + str(fixture.root / "authorization.db")
    direct_checks(fixture)
    if mode == "benchmark":
        benchmark_backend(fixture)
    elif mode == "mounted":
        try:
            run_mounted_tests(fixture)
        except RuntimeError as exc:
            mounted_errors.append(fixture.env["FIXTURE_BACKEND"] + ": " + str(exc))
    elif mode not in ("baseline", "firestore"):
        cached_checks(fixture, "redis" if mode == "all" else mode)

    if mode == "all":
        run_mounted_tests(fixture)
    if mode in ("firestore", "all", "benchmark"):
        fixture.start_emulator()
        work = fixture.root / "go.work"
        fixture.env["GOWORK"] = str(work)
        try:
            directory = fixture.root / "source/pockets/authorization/stores/firestore"
            output = fixture.run(["go", "test", "-tags=integration", "-json", "-count=1", "-run", "^TestCache", "./..."], cwd=directory)
            events = [json.loads(line) for line in output.splitlines() if line.strip()]
            if any(event.get("Action") == "skip" for event in events) or not any(event.get("Action") == "pass" and event.get("Test") for event in events):
                raise RuntimeError("Firestore adapter cache tests absent/skipped")
        finally:
            fixture.env["GOWORK"] = "off"
        fixture.env["FIXTURE_BACKEND"] = "firestore"
        direct_checks(fixture)
        if mode == "benchmark":
            benchmark_backend(fixture)
        else:
            cached_checks(fixture, "redis")
    if mounted_errors:
        raise RuntimeError("mounted regression failures: " + " | ".join(mounted_errors))


def run_adapter_cache_tests(fixture):
    work = fixture.root / "go.work"
    work.write_text("go 1.26.1\n\nuse (\n" + "\n".join(f' "{fixture.root / "source" / module}"' for module in MODULES) + "\n)\n")
    fixture.env["GOWORK"] = str(work)
    try:
        for backend in ("pgx", "turso"):
            directory = fixture.root / "source/pockets/authorization/stores" / backend
            output = fixture.run(["go", "test", "-json", "-count=1", "-run", "^TestCache", "./..."], cwd=directory)
            events = [json.loads(line) for line in output.splitlines() if line.strip()]
            skipped = [event.get("Test") for event in events if event.get("Action") == "skip"]
            passed = [event.get("Test") for event in events if event.get("Action") == "pass" and event.get("Test")]
            if skipped or not passed:
                raise RuntimeError(f"{backend} cache tests skipped or absent: {skipped}")
            fixture.events.append({"adapter_cache_tests": backend, "passed": len(passed), "skipped": skipped})
    finally:
        fixture.env["GOWORK"] = "off"


def direct_checks(fixture):
    fixture.events.append({"baseline_backend": fixture.env["FIXTURE_BACKEND"]})
    fixture.run([fixture.root / "app", "migrate"])
    apps = [fixture.start([fixture.root / "app"], f"app-{fixture.env['FIXTURE_BACKEND']}-{i}", control=True) for i in range(2)]
    for app in apps:
        fixture.control(app, "barrier")
        if fixture.control(app, "check")["Allowed"]:
            raise AssertionError("empty authority granted")
    fixture.control(apps[0], "grant")
    for app in apps:
        if not fixture.control(app, "check")["Allowed"]:
            raise AssertionError("committed grant not visible across processes")
    fixture.control(apps[1], "revoke")
    for app in apps:
        if fixture.control(app, "check")["Allowed"]:
            raise AssertionError("committed revoke not visible across processes")


def cached_checks(fixture, mode):
    fixture.run([fixture.root / "app", "migrate-cache"])
    fixture.env["FIXTURE_CACHE"] = ""
    if fixture.env["FIXTURE_BACKEND"] == "firestore":
        fixture.env["FIXTURE_FS_PARTICIPATE"] = "1"
    writer = fixture.start([fixture.root / "app"], "uncached-writer-" + fixture.env["FIXTURE_BACKEND"], control=True)
    fixture.control(writer, "grant")
    for cache in (("lru", "redis") if mode == "redis" else ("lru",)):
        fixture.env["FIXTURE_CACHE"] = cache
        fixture.env["FIXTURE_NAMESPACE"] = fixture.token + "/" + fixture.env["FIXTURE_BACKEND"] + "/" + cache
        apps = [fixture.start([fixture.root / "app"], f"cached-{fixture.env['FIXTURE_BACKEND']}-{cache}-{i}", control=True) for i in range(2)]
        fixture.events.append({"cache_leg": cache, "authority": fixture.env["FIXTURE_BACKEND"]})
        for app in apps:
            cold = fixture.control(app, "check")
            if not cold["Allowed"] or cold["Stats"]["Ready"] or cold["Stats"]["BypassCold"] < 1:
                raise AssertionError("cold runtime did not use direct authority")
            fixture.control(app, "poll")
        for index, app in enumerate(apps):
            initial = fixture.control(app, "stats")
            first = fixture.control(app, "check")
            if cache == "redis" and index == 1 and first["Snapshots"] != initial["Snapshots"]:
                raise AssertionError("second process did not reuse shared Redis tuple entry")
            before = fixture.control(app, "stats")
            warm = fixture.control(app, "check")
            if not warm["Allowed"] or warm["Stats"]["HitComplete"] <= before["Stats"]["HitComplete"] or warm["Snapshots"] != before["Snapshots"] or warm["Observations"] != before["Observations"]:
                raise AssertionError("warm Check did not complete without durable snapshot/observation")
            before_batch = fixture.control(app, "stats")
            partial = fixture.control(app, "check_batch")
            if (cache == "lru" or index == 0) and partial["Snapshots"] != before_batch["Snapshots"] + 1:
                raise AssertionError("partial heterogeneous batch did not use one whole snapshot")
            before = fixture.control(app, "stats")
            batch = fixture.control(app, "check_batch")
            if batch["Results"] != [True, False, True] or batch["Snapshots"] != before["Snapshots"] or batch["Stats"]["HitComplete"] <= before["Stats"]["HitComplete"]:
                raise AssertionError("warm batch parity/hit failed")
            explained = fixture.control(app, "explain")
            if not explained["Allowed"] or explained["Snapshots"] != batch["Snapshots"] or explained["Stats"]["HitComplete"] <= batch["Stats"]["HitComplete"]:
                raise AssertionError("CheckExplain disagreed with Check")
        fixture.control(writer, "revoke")
        for app in apps:
            fixture.control(app, "poll")
            if fixture.control(app, "check")["Allowed"]:
                raise AssertionError("polled revocation still grants")
        fixture.control(writer, "grant")
        for app in apps:
            fixture.control(app, "poll")
            fixture.control(app, "check")
        fixture.control(writer, "revoke")
        fixture.control(apps[0], "await_expiry")
        for app in apps:
            expired = fixture.control(app, "check")
            if expired["Allowed"] or expired["Stats"]["BypassExpired"] < 1:
                raise AssertionError("expired observation admitted stale grant")
        fixture.control(writer, "grant")
        for app in apps:
            fixture.control(app, "poll")
            fixture.control(app, "cache_fail")
            failed = fixture.control(app, "check")
            if not failed["Allowed"] or failed["Stats"]["CacheErrors"] < 1:
                raise AssertionError("cache outage did not fall back to authority")
            fixture.control(app, "cache_recover")
            fixture.control(app, "check")
            before = fixture.control(app, "stats")
            recovered = fixture.control(app, "check")
            if not recovered["Allowed"] or recovered["Stats"]["HitComplete"] <= before["Stats"]["HitComplete"]:
                raise AssertionError("byte cache did not recover")
    fixture.env["FIXTURE_CACHE"] = ""
    fixture.control(writer, "revoke")


def run_mounted_tests(fixture, benchmark=False):
    fixture.env["FIXTURE_MOUNTED"] = "1"
    if benchmark:
        fixture.env["FIXTURE_MOUNTED_BENCHMARK"] = "1"
    try:
        def execute(_):
            return fixture.run(["go", "test", "-json", "-count=1", "-run", "^TestMounted", "./..."], cwd=fixture.root / "host")
        if benchmark:
            with ThreadPoolExecutor(max_workers=2) as executor:
                outputs = list(executor.map(execute, range(2)))
        else:
            outputs = [execute(0)]
        for output in outputs:
            events = [json.loads(line) for line in output.splitlines() if line.strip()]
            if any(event.get("Action") == "skip" for event in events):
                raise RuntimeError("mounted tests skipped")
            passed = [event.get("Test") for event in events if event.get("Action") == "pass" and event.get("Test")]
            if len(passed) < 4:
                raise RuntimeError("mounted direct/LRU/Redis cases did not all run")
            fixture.events.append({"mounted_backend": fixture.env["FIXTURE_BACKEND"], "passed": passed})
            for event in events:
                line = event.get("Output", "")
                if "MOUNTED_BENCHMARK " in line:
                    fixture.events.append({"mounted_benchmark": json.loads(line.split("MOUNTED_BENCHMARK ", 1)[1])})
    finally:
        fixture.env.pop("FIXTURE_MOUNTED", None)
        fixture.env.pop("FIXTURE_MOUNTED_BENCHMARK", None)


def benchmark_backend(fixture):
    backend = fixture.env["FIXTURE_BACKEND"]
    if backend not in fixture.benchmark_backends:
        fixture.events.append({"benchmark_omitted": backend, "reason": "explicit backend selection"})
        return
    fixture.env["FIXTURE_CACHE"] = ""
    paths = ["allow", "deny", "through", "role", "explain", "filter", "partial", "outage"] + [f"batch{n}{suffix}" for n in (1, 10, 100, 500) for suffix in ("", "mixed")]
    direct = [fixture.start([fixture.root / "app"], f"benchmark-{backend}-direct-{i}", control=True) for i in range(2)]
    fixture.control(direct[0], "grant")
    fixture.control(direct[0], "benchmark_setup", timeout=90)
    def measure(apps, operation, path=""):
        with ThreadPoolExecutor(max_workers=2) as executor:
            return list(executor.map(lambda app: fixture.control(app, operation, timeout=90, workload=path)["Benchmark"], apps))
    def matrix(apps, warm=False):
        result = {}
        for path in paths:
            rows = []
            for repeat in range(3):
                if warm:
                    for app in apps:
                        fixture.control(app, "poll")
                rows.append(measure(apps, "benchmark_check", path))
            result[path] = rows
        return result
    direct_reads = matrix(direct)
    direct_writes = [measure(direct, "benchmark_write") for _ in range(3)]
    fixture.run([fixture.root / "app", "migrate-cache"])
    participating = direct
    if backend == "firestore":
        fixture.env["FIXTURE_FS_PARTICIPATE"] = "1"
        participating = [fixture.start([fixture.root / "app"], f"benchmark-{backend}-participating-{i}", control=True) for i in range(2)]
    writes_with_invalidation = [measure(participating, "benchmark_write") for _ in range(3)]
    def median_p95(rows, process):
        return sorted(row[process]["P95NS"] for row in rows)[len(rows)//2]
    results = {"authority": backend, "direct": direct_reads, "writes_before": direct_writes, "writes_after": writes_with_invalidation,
               "load": "2 independent processes; fixed arrivals 50 reads/s each, batch100/500 20/s each, writes50/s each; 5 warmups per read run; 3 repeats; arrival-inclusive latency",
               "sample_counts": "60 reads per process/repeat or 30 for batch100/500; 100 writes per process/repeat"}
    results["write_p95_within_10_percent"] = all(median_p95(writes_with_invalidation, i) <= median_p95(direct_writes, i) * 1.10 for i in range(2))
    for cache in ("lru", "redis"):
        fixture.env["FIXTURE_CACHE"] = cache
        fixture.env["FIXTURE_NAMESPACE"] = fixture.token + "/benchmark/" + backend + "/" + cache
        apps = [fixture.start([fixture.root / "app"], f"benchmark-{backend}-{cache}-{i}", control=True) for i in range(2)]
        bypass = matrix(apps)
        warm = matrix(apps, warm=True)
        improvement = {path: all(median_p95(warm[path], i) <= median_p95(direct_reads[path], i) * .80 for i in range(2)) for path in paths if path not in ("filter", "partial", "outage")}
        results[cache] = {"bypass": bypass, "warm": warm, "warm_p95_improves_20_percent": improvement}
    fixture.env["FIXTURE_CACHE"] = ""
    results["operation_errors"] = sum(sum(row.get("Errors", {}).values()) for group in [direct_writes, writes_with_invalidation] + list(direct_reads.values()) + [rows for cache in ("lru", "redis") for phase in ("bypass", "warm") for rows in results[cache][phase].values()] for repeat in group for row in repeat)
    results["warm_policy"] = "default fill limits; warm means prepolled with 5 warmups, not guaranteed complete hits; consult Hits/Count, especially capacity-limited batch500"
    results["provisional_acceptance"] = results["operation_errors"] == 0 and results["write_p95_within_10_percent"] and all(all(results[cache]["warm_p95_improves_20_percent"].values()) for cache in ("lru", "redis"))
    fixture.events.append({"benchmark": results})
    if backend in ("pgx", "turso"):
        try:
            run_mounted_tests(fixture, benchmark=True)
        except RuntimeError as exc:
            fixture.events.append({"mounted_benchmark_failure": str(exc)})
    fixture.events.append({"benchmark_scope": "local fixed-load decision paths plus SQL mounted Live+permission HTTP; physical RTT/lock waits, network placements, saturation curves, cache persistence/process pause recovery, Firestore mounted HTTP and real GCP unmeasured"})


def run_live_tests(fixture, source, config):
    generate(fixture, source)
    work = fixture.root / "go.work"
    work.write_text("go 1.26.1\n\nuse (\n" + "\n".join(f' "{fixture.root / "source" / module}"' for module in MODULES) + "\n)\n")
    fixture.env.update(config)
    fixture.env["GOWORK"] = str(work)
    directory = fixture.root / "source/pockets/authorization/stores/firestore"
    output = fixture.run(["go", "test", "-tags=integration,live", "-json", "-count=1", "-run", "^TestCache.*Live$", "./..."], cwd=directory, timeout=1800)
    events = [json.loads(line) for line in output.splitlines() if line.strip()]
    roots = {event.get("Test") for event in events if event.get("Action") == "pass" and event.get("Test") and "/" not in event["Test"]}
    if any(event.get("Action") == "skip" for event in events) or len(roots) < 5:
        raise RuntimeError("live cache roots skipped or missing; live certification failed")
    fixture.events.append({"live_cache_roots": sorted(roots), "project": config["FIRESTORE_LIVE_PROJECT_ID"], "database": config["FIRESTORE_LIVE_DATABASE_ID"]})


def benchmark_acceptance(events):
    results = [event["benchmark"] for event in events if "benchmark" in event]
    mounted_errors = sum(sum(event["mounted_benchmark"]["measurements"].get("Errors", {}).values())
                         for event in events if "mounted_benchmark" in event)
    return bool(results) and all(result["provisional_acceptance"] for result in results) and mounted_errors == 0 and not any("mounted_benchmark_failure" in event for event in events)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("baseline", "sql", "mounted", "redis", "firestore", "benchmark", "firestore-live", "all"), default="sql")
    parser.add_argument("--benchmark-backends", nargs="+", choices=("pgx", "turso", "firestore"), default=["pgx", "turso", "firestore"])
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--postgres-bin", default="/opt/homebrew/opt/postgresql@17/bin")
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args(argv)
    report = {"mode": args.mode, "status": "failed", "coverage": "SQL adapter tests and direct baselines; requested cache legs recorded in events; cloud routes unverified"}
    fixture = None
    try:
        if args.mode not in ("baseline", "sql", "redis", "mounted", "firestore", "benchmark", "firestore-live", "all"):
            raise NotImplementedError(f"mode {args.mode} is not implemented; no conformance claim")
        live = live_environment(os.environ) if args.mode == "firestore-live" else None
        fixture = OwnedFixture()
        fixture.benchmark_backends = args.benchmark_backends
        if live is not None:
            run_live_tests(fixture, args.source.resolve(), live)
        else:
            sql_baseline(fixture, args.source.resolve(), args.postgres_bin, args.mode)
        report["status"] = "passed"
        if args.mode == "benchmark":
            report["provisional_acceptance"] = benchmark_acceptance(fixture.events)
            report["measurement_status"] = "completed"
            if not report["provisional_acceptance"]:
                report["status"] = "thresholds-not-met"
    except Exception as exc:
        report["error"] = str(exc)
    finally:
        if fixture:
            report["events"] = fixture.events
            report["fixture_sources"] = {p.name: p.read_text() for p in (fixture.root / "host").glob("*") if p.is_file()}
            try:
                fixture.cleanup()
                report["cleanup"] = "passed"
            except Exception as exc:
                report.update(status="failed", cleanup=str(exc))
        args.report.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({key: value for key, value in report.items() if key not in ("events", "fixture_sources")}))
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
