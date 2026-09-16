#!/usr/bin/env python3
"""Verify canonical authorization against owned local PostgreSQL, SQLite and Redis.

No caller-supplied datastore endpoint is accepted. Snapshots source modules before
execution, verifies PostgreSQL process/data identity, runs real adapter/HTTP tests
and optionally repeated Go benchmarks, then cleans only owned resources.
The retired byte-cache/LRU generated-host protocol is no longer supported.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import signal
import shutil
import socket
import subprocess
import tempfile
import time
import threading
import uuid

MODULES = ('examples/auth-cms', 'examples/cms', 'examples/goth-showcase', 'examples/jobs-minimal', 'examples/minimal', 'integrations/cryptids/bcrypt', 'integrations/cryptids/golang-jwt', 'integrations/cryptids/google-uuid', 'integrations/datastores/firestore', 'integrations/datastores/pgxdb', 'integrations/datastores/turso', 'integrations/email/sendgrid', 'integrations/filestorage/gcs', 'integrations/filestorage/s3', 'integrations/kvstores/goredis', 'integrations/oauth/github', 'integrations/oauth/google', 'integrations/scheduling/robfig-cron', 'integrations/tracing/otel', 'pockets', 'pockets/authentication', 'pockets/authentication/stores/firestore', 'pockets/authentication/stores/pgx', 'pockets/authentication/stores/turso', 'pockets/authentication/views/goth', 'pockets/authorization', 'pockets/authorization/stores/goredis', 'pockets/authorization/stores/pgx', 'pockets/authorization/stores/turso', 'pockets/cms', 'pockets/cms/stores/pgx', 'pockets/cms/stores/turso', 'pockets/cms/views/goth', 'pockets/events', 'pockets/events/stores/pgx', 'pockets/events/stores/turso', 'pockets/jobs', 'pockets/jobs/stores/pgx', 'pockets/jobs/stores/turso', 'sdk', 'ui/goth', 'workshop/gopernicus')

def clean_environment(parent, root):
    # Allowlist rather than stripping known database/provider variable names.
    env = {key: parent[key] for key in ("PATH", "HOME", "TMPDIR", "SYSTEMROOT") if key in parent}
    env.update(GOWORK="off", GOENV="off", GOPROXY="off", GOTOOLCHAIN="local",
               GOCACHE=str(root / "go-cache"), LC_ALL="C", LANG="C")
    return env


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
        for log in self.logs:
            log.close()
        if (self.root / "owner").read_text() != self.token:
            raise RuntimeError("ownership marker mismatch: preserving directory")
        shutil.rmtree(self.root)


def generate(fixture, source):
    """Snapshot workspace modules without copying local environment or VCS files."""
    pinned = fixture.root / "source"
    digest = hashlib.sha256()
    for module in MODULES:
        original = source / module
        destination = pinned / module
        destination.mkdir(parents=True, exist_ok=True)
        for directory, subdirs, filenames in os.walk(original):
            current = Path(directory)
            subdirs[:] = sorted(name for name in subdirs if not name.startswith(".")
                                and name not in ("node_modules", "vendor", "__pycache__")
                                and not (current / name).is_symlink()
                                and not (current / name / "go.mod").exists())
            for name in sorted(filenames):
                path = current / name
                if name.startswith(".") or path.is_symlink():
                    continue
                relative = path.relative_to(original)
                target = destination / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                data = path.read_bytes()
                target.write_bytes(data)
                digest.update(str(path.relative_to(source)).encode() + b"\0" + data)
    # Use exactly this snapshot rather than the caller's ambient workspace.
    workspace = source / "go.work"
    if workspace.exists():
        (pinned / "go.work").write_bytes(workspace.read_bytes())
    fixture.events.append({"source_sha256": digest.hexdigest(), "modules": list(MODULES)})
    return pinned


def checked_tests(fixture, module, extra=(), require=(), reject_skips=False):
    output = fixture.run(["go", "test", "-json", "-race", "-count=1", *extra, "./..."],
                         cwd=fixture.root / "source" / module, timeout=600)
    events = [json.loads(line) for line in output.splitlines() if line.startswith("{")]
    passed = {e.get("Test") for e in events if e.get("Action") == "pass" and e.get("Test")}
    skipped = [e.get("Test") for e in events if e.get("Action") == "skip"]
    if not passed or any(name not in passed for name in require):
        raise RuntimeError(f"{module}: required tests absent or skipped: {require}")
    if reject_skips and skipped:
        raise RuntimeError(f"{module}: fixture-bound tests skipped: {skipped}")
    fixture.events.append({"suite": module, "schema": fixture.env.get("POSTGRES_TEST_SCHEMA", "public"),
                           "passed": len(passed), "skipped": skipped})
    return skipped


def run_adapter_cache_tests(fixture):
    common = ("TestConformance", "TestTransactional", "TestCheckAmbient", "TestLookupAmbient",
              "TestFreshCanonicalSchema", "TestFreshCacheInstallationOrderAndRollback",
              "TestCanonicalConstructorsRejectPartialSchemas")
    configured = fixture.env.pop("POSTGRES_TEST_SCHEMA", None)
    try:
        for schema in (None, "authorization_named"):
            if schema:
                fixture.env["POSTGRES_TEST_SCHEMA"] = schema
            checked_tests(fixture, "pockets/authorization/stores/pgx", require=common + (
                "TestCollationControlsOrdering_NonC", "TestCanonicalSQLRejectsWeakAmbientSnapshot",
                "TestTupleSourceNumericEventOrderAcrossDigits"), reject_skips=True)
    finally:
        fixture.env.pop("POSTGRES_TEST_SCHEMA", None)
        if configured:
            fixture.env["POSTGRES_TEST_SCHEMA"] = configured
    checked_tests(fixture, "pockets/authorization/stores/turso", ("-tags=integration",),
                  require=common + ("TestTupleCacheLiveSnapshots",), reject_skips=True)
    checked_tests(fixture, "pockets/authorization/stores/goredis", ("-tags=integration",),
                  require=("TestSQLRedisDeliveryRecovery", "TestSQLRedisModelFreeRoleReads",
                           "TestSQLRedisEventOrderPreservesFinalRevocation"), reject_skips=True)


def run_mounted_tests(fixture):
    checked_tests(fixture, "examples/auth-cms",
                  require=("TestInvitationMemberAndOwnerCoexist", "TestDemoAuditRouteUsesScopedPredicate",
                           "TestRoleRoutesPlatformAdminDrivesTheLifecycle", "TestMembershipPolicySharesSnapshotAndFailsClosed"))


def sql_baseline(fixture, source, postgres_bin, mode="all"):
    pinned = generate(fixture, source)
    fixture.env["GOWORK"] = str(pinned / "go.work")
    pg = Path(postgres_bin)
    fixture.env["PATH"] = str(pg) + os.pathsep + fixture.env.get("PATH", "")
    version = fixture.run([pg / "postgres", "--version"])
    if " 17." not in version:
        raise ValueError("fixture requires PostgreSQL 17")
    data = fixture.root / "pgdata"
    fixture.run([pg / "initdb", "-D", data, "-U", "fixture", "-A", "trust",
                 "--encoding=UTF8", "--locale=en_US.UTF-8"])
    port = fixture.port("postgres")
    process = fixture.start([pg / "postgres", "-D", data, "-h", "127.0.0.1", "-p", port,
                             "-k", str(fixture.root)], "postgres")
    fixture.wait_ready("postgres", process, None)
    identity = fixture.run([pg / "psql", "-h", "127.0.0.1", "-p", port, "-U", "fixture",
                            "-d", "postgres", "-Atc", "SHOW data_directory"]).strip()
    if Path(identity).resolve() != data.resolve() or process.poll() is not None:
        raise RuntimeError("PostgreSQL ownership identity mismatch")
    fixture.run([pg / "createdb", "-h", "127.0.0.1", "-p", port, "-U", "fixture", "authorization_fixture"])
    dsn = f"postgres://fixture@127.0.0.1:{port}/authorization_fixture?sslmode=disable"
    sqlite = "file:" + str(fixture.root / "authorization.db")
    fixture.env.update(POSTGRES_TEST_DSN=dsn, POSTGRES_NON_C_TEST_DSN=dsn,
                       POSTGRES_TEST_SCHEMA="authorization_named", AUTHORIZATION_LISTING_TEST_DSN=dsn,
                       TURSO_DATABASE_URL=sqlite, AUTHORIZATION_TURSO_DISPOSABLE_URL=sqlite)
    if mode == "benchmark":
        run_benchmarks(fixture)
    else:
        checked_tests(fixture, "integrations/datastores/pgxdb", require=(
            "TestTransactSnapshotCoherentReadsAndPendingWrites", "TestSnapshotIsolationActualTransaction",
            "TestTransactSnapshotRollback"), reject_skips=True)
        checked_tests(fixture, "pockets/authorization")
        if mode in ("sql", "all"):
            run_adapter_cache_tests(fixture)
        if mode in ("mounted", "all"):
            run_mounted_tests(fixture)


def benchmark_cases(module):
    """Exact workload inventory; a skipped backend must not shrink the matrix."""
    backlog = {f"BenchmarkTupleSourceBacklog/transient-tuples={size}/full={full}"
               for size in (0, 1000, 10000) for full in ("false", "true")}
    if module == "pockets/authorization":
        return (
            {f"BenchmarkCanonicalRoleReads/roles={size}/{mode}" for size in (1, 16, 128) for mode in ("durable", "cold", "warm")}
            | {f"BenchmarkTupleCachePublicationOverlap/{mode}" for mode in ("warm", "idle_poll_during_read", "unrelated_publication_during_read", "capacity_fallback")}
            | {f"BenchmarkRoleBatchReads/requests_{size}" for size in (1, 20, 128)}
            | {f"BenchmarkCheckBatchThrough/container-{mode}" for mode in ("direct", "through")}
            | {f"Benchmark{family}/n={size}" for family in ("FilterAuthorizedDeniedCandidates", "CheckBatchDeniedCandidates") for size in (50, 300)}
            | {f"BenchmarkComposableGuard/{policy}" for policy in ("global", "two_exact_roles", "mixed")}
        )
    if module.endswith("/pgx"):
        return (backlog | {f"BenchmarkDecisionsPostgres/{op}" for op in ("direct", "through", "batch", "filter")}
                | {f"BenchmarkTupleWriterContentionPostgres/tenants={n}" for n in (1, 8)})
    if module.endswith("/turso"):
        return (backlog | {f"BenchmarkThroughBatchSQLite/{mode}/{op}" for mode in ("sequential", "batched") for op in ("direct", "through", "batch", "filter")}
                | {f"BenchmarkTupleWriterContentionSQLite/tenants={n}" for n in (1, 8)})
    if module.endswith("/goredis"):
        return (
            {f"BenchmarkTupleCache/tuples={size}/{op}" for size in (100, 1000, 10000, 100000) for op in ("read_allowed", "read_rejected", "delta_allowed", "delta_rejected", "rebuild")}
            | {f"BenchmarkSQLRedisDecisions/{db}/{mode}/{op}" for db in ("sqlite", "postgres") for mode in ("sql", "cold", "warm") for op in ("check", "batch128", "filter128")}
            | {f"BenchmarkSQLRedisRoleReads/{db}/roles={size}/{mode}" for db in ("sqlite", "postgres") for size in (1, 16, 128) for mode in ("sql", "cold", "warm")}
        )
    raise ValueError("unknown benchmark module")


def parse_benchmarks(output, expected):
    samples, current = {}, None
    for line in output.splitlines():
        fields = line.split()
        if not fields:
            continue
        if fields[0].startswith("Benchmark"):
            current = re.sub(r"-\d+$", "", fields[0])
        if "ns/op" not in fields:
            continue
        # Go may print the name before setup emits migration logs. Keep that
        # name until its metrics arrive on a later line.
        metrics = {unit: float(value) for value, unit in re.findall(r"([0-9.eE+-]+)\s+([A-Za-z_]+/op)", line)}
        if current is None or not {"ns/op", "B/op", "allocs/op"}.issubset(metrics):
            raise RuntimeError("unattributed or incomplete benchmark metrics")
        samples.setdefault(current, []).append(metrics)
    missing, extra = expected - samples.keys(), samples.keys() - expected
    incomplete = {name: len(values) for name, values in samples.items() if len(values) != 5}
    if missing or extra or incomplete:
        raise RuntimeError(f"incomplete benchmark matrix: missing={sorted(missing)}, extra={sorted(extra)}, samples={incomplete}")
    return samples


def run_benchmarks(fixture):
    jobs = (
        ("pockets/authorization", (), "Benchmark(CanonicalRoleReads|TupleCachePublicationOverlap|RoleBatchReads|CheckBatchThrough|FilterAuthorizedDeniedCandidates|CheckBatchDeniedCandidates|ComposableGuard)$"),
        ("pockets/authorization/stores/pgx", (), "Benchmark(DecisionsPostgres|TupleSourceBacklog|TupleWriterContentionPostgres)"),
        ("pockets/authorization/stores/turso", (), "Benchmark(ThroughBatchSQLite|TupleSourceBacklog|TupleWriterContentionSQLite)"),
        ("pockets/authorization/stores/goredis", ("-tags=integration",), "Benchmark(TupleCache|SQLRedis)"),
    )
    for module, tags, pattern in jobs:
        print(f"Benchmarking {module}: {len(benchmark_cases(module))} cases, five 1s samples each", flush=True)
        output = fixture.run(["go", "test", *tags, "-run=^$", "-bench="+pattern, "-benchmem",
                              "-benchtime=1s", "-count=5", "./..."],
                             cwd=fixture.root / "source" / module, timeout=3600)
        samples = parse_benchmarks(output, benchmark_cases(module))
        fixture.events.append({"benchmarks": module, "count": 5, "benchtime": "1s", "samples": samples, "output": output})
        print(f"Completed {module}: {len(samples)} cases", flush=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("sql", "mounted", "benchmark", "all"), default="all")
    parser.add_argument("--source", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--postgres-bin", default="/opt/homebrew/opt/postgresql@17/bin")
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args(argv)
    report = {"mode": args.mode, "status": "failed",
              "coverage": "owned local canonical SQL/Redis/HTTP suites; remote Turso unverified"}
    fixture = None
    try:
        fixture = OwnedFixture()
        sql_baseline(fixture, args.source.resolve(), args.postgres_bin, args.mode)
        report["status"] = "passed"
    except Exception as exc:
        report["error"] = str(exc)
    finally:
        if fixture:
            report["events"] = fixture.events
            try:
                fixture.cleanup()
                report["cleanup"] = "passed"
            except Exception as exc:
                report.update(status="failed", cleanup=str(exc))
        args.report.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({key: value for key, value in report.items() if key != "events"}))
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
