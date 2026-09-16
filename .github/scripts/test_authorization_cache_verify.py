from pathlib import Path
import json
import subprocess
import socket
import sys
import tempfile
import unittest

from authorization_cache_verify import OwnedFixture, clean_environment, generate, main, run_adapter_cache_tests, run_mounted_tests, checked_tests, parse_benchmarks, benchmark_cases
from unittest.mock import patch


class OwnedFixtureTests(unittest.TestCase):
    def test_environment_is_allowlisted(self):
        env = clean_environment({"PATH": "/bin", "DATABASE_URL": "production", "POSTGRES_TEST_DSN": "production", "GOFLAGS": "-modfile=unsafe", "GOENV": "unsafe", "GOOGLE_APPLICATION_CREDENTIALS": "secret", "FIRESTORE_EMULATOR_HOST": "production"}, Path("/tmp/owned"))
        self.assertEqual(env["PATH"], "/bin")
        self.assertEqual(env["GOENV"], "off")
        self.assertEqual(env["GOWORK"], "off")
        for key in ("DATABASE_URL", "POSTGRES_TEST_DSN", "GOFLAGS", "GOOGLE_APPLICATION_CREDENTIALS", "FIRESTORE_EMULATOR_HOST"):
            self.assertNotIn(key, env)

    def test_endpoints_require_exact_ownership(self):
        fixture = OwnedFixture()
        try:
            port = 34567
            fixture.endpoints["postgres"] = ("127.0.0.1", port)
            self.assertEqual(fixture.endpoint("postgres", "127.0.0.1", port), ("127.0.0.1", port))
            for name, host, value in (("redis", "127.0.0.1", port), ("postgres", "localhost", port), ("postgres", "example.com", port), ("postgres", "127.0.0.1", port + 1)):
                with self.assertRaises(ValueError):
                    fixture.endpoint(name, host, value)
        finally:
            fixture.cleanup()

    def test_cleanup_stops_only_owned_process(self):
        outsider = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
        fixture = OwnedFixture()
        try:
            child = fixture.start([sys.executable, "-c", "import time; time.sleep(60)"], "child")
            fixture.cleanup()
            self.assertIsNotNone(child.poll())
            self.assertIsNone(outsider.poll())
            self.assertFalse(fixture.root.exists())
        finally:
            outsider.terminate()
            outsider.wait(timeout=5)

    def test_run_timeout_stops_descendants(self):
        fixture = OwnedFixture()
        try:
            # Descendant holds a loopback listener and inherits stdout. Killing only
            # the parent would leave both the listener and communicate pipe alive.
            child = "import socket,time; from pathlib import Path; s=socket.socket(); s.bind(('127.0.0.1',0)); s.listen(); Path('child-port').write_text(str(s.getsockname()[1])); time.sleep(60)"
            parent = "import subprocess,sys,time; from pathlib import Path; subprocess.Popen([sys.executable,'-c',sys.argv[1]]);\nwhile not Path('child-port').exists(): time.sleep(.01)\ntime.sleep(60)"
            with self.assertRaises(subprocess.TimeoutExpired):
                fixture.run([sys.executable, "-c", parent, child], timeout=1)
            port = int((fixture.root / "child-port").read_text())
            with socket.socket() as probe:
                probe.bind(("127.0.0.1", port))
        finally:
            fixture.cleanup()

    def test_cleanup_refuses_changed_marker(self):
        fixture = OwnedFixture()
        (fixture.root / "owner").write_text("different-owner")
        with self.assertRaises(RuntimeError):
            fixture.cleanup()
        self.assertTrue(fixture.root.exists())
        (fixture.root / "owner").write_text(fixture.token)
        fixture.cleanup()

    def test_control_timeout_and_cleanup(self):
        fixture = OwnedFixture()
        try:
            child = fixture.start([sys.executable, "-c", "import sys,time; sys.stdin.readline(); print('{',end='',flush=True); time.sleep(60)"], "partial", control=True)
            with self.assertRaises(TimeoutError):
                fixture.control(child, "barrier", timeout=.1)
        finally:
            fixture.cleanup()
        self.assertIsNotNone(child.poll())

    def test_failure_path_cleans_fixture(self):
        with tempfile.TemporaryDirectory() as root:
            fixture = OwnedFixture()
            with patch("authorization_cache_verify.OwnedFixture", return_value=fixture), patch("authorization_cache_verify.sql_baseline", side_effect=RuntimeError("injected")):
                self.assertEqual(main(["--report", str(Path(root) / "report.json")]), 1)
            self.assertFalse(fixture.root.exists())

    def test_generate_excludes_environment_and_pins_content(self):
        with tempfile.TemporaryDirectory() as root:
            source = Path(root)
            (source / "sdk").mkdir()
            (source / "sdk/go.mod").write_text("module example")
            (source / "sdk/.env").write_text("SECRET=production")
            fixture = OwnedFixture()
            try:
                with patch("authorization_cache_verify.MODULES", ("sdk",)):
                    generate(fixture, source)
                self.assertFalse((fixture.root / "source/sdk/.env").exists())
                (source / "sdk/go.mod").write_text("changed")
                self.assertEqual((fixture.root / "source/sdk/go.mod").read_text(), "module example")
                self.assertEqual(len(fixture.events[-1]["source_sha256"]), 64)
            finally:
                fixture.cleanup()

    def test_adapter_tests_require_named_proofs_and_both_schemas(self):
        fixture = OwnedFixture()
        try:
            calls=[]
            def checked(f, module, extra=(), require=(), reject_skips=False):
                self.assertTrue(require)
                self.assertTrue(reject_skips)
                calls.append((module,f.env.get("POSTGRES_TEST_SCHEMA"),extra))
            with patch("authorization_cache_verify.checked_tests", side_effect=checked):
                run_adapter_cache_tests(fixture)
            self.assertEqual([c[1] for c in calls[:2]], [None,"authorization_named"])
            self.assertIn("-tags=integration",calls[2][2])
        finally:
            fixture.cleanup()

    def test_required_tests_and_nested_skips_are_not_success(self):
        fixture = OwnedFixture()
        try:
            cases = [
                [{"Action":"pass","Test":"TestOther"}],
                [{"Action":"pass","Test":"TestProof"},{"Action":"skip","Test":"TestProof/required"}],
            ]
            for events in cases:
                with patch.object(fixture, "run", return_value="\n".join(map(json.dumps,events))):
                    with self.assertRaises(RuntimeError):
                        checked_tests(fixture,"module",require=("TestProof",),reject_skips=True)
        finally:
            fixture.cleanup()

    def test_benchmark_matrix_rejects_missing_or_short_samples(self):
        name="BenchmarkTupleCache/tuples=100/read_allowed"
        row=f"{name}-14 100 12.5 ns/op 24 B/op 2 allocs/op\n"
        self.assertEqual(len(parse_benchmarks(row*5,{name})[name]),5)
        for output, expected in [(row,{name}), (row*5,{name,"BenchmarkMissingRedis"}), (row*6,{name})]:
            with self.assertRaises(RuntimeError):
                parse_benchmarks(output,expected)
        interleaved=f"{name}-14 2026/09/15 migration log\nnext log\n100 12.5 ns/op 24 B/op 2 allocs/op\n"
        self.assertEqual(len(parse_benchmarks(interleaved*5,{name})[name]),5)
        modules=["pockets/authorization", "pockets/authorization/stores/pgx", "pockets/authorization/stores/turso", "pockets/authorization/stores/goredis"]
        self.assertEqual([len(benchmark_cases(m)) for m in modules],[25,12,16,56])

    def test_mounted_requires_named_behavior_proofs(self):
        fixture = OwnedFixture()
        try:
            with patch.object(fixture, "run", return_value='{"Action":"pass","Test":"TestUnrelated"}'):
                with self.assertRaises(RuntimeError):
                    run_mounted_tests(fixture)
            output = "\n".join(json.dumps({"Action":"pass", "Test":name}) for name in
                ("TestInvitationMemberAndOwnerCoexist", "TestDemoAuditRouteUsesScopedPredicate", "TestRoleRoutesPlatformAdminDrivesTheLifecycle", "TestMembershipPolicySharesSnapshotAndFailsClosed"))
            with patch.object(fixture, "run", return_value=output):
                run_mounted_tests(fixture)
            self.assertEqual(fixture.events[-1]["suite"], "examples/auth-cms")
        finally:
            fixture.cleanup()


if __name__ == "__main__":
    unittest.main()
