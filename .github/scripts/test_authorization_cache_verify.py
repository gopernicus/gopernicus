from pathlib import Path
import subprocess
import socket
import sys
import tempfile
import unittest

from authorization_cache_verify import OwnedFixture, clean_environment, generate, main, run_adapter_cache_tests, run_mounted_tests, live_environment, benchmark_acceptance
from unittest.mock import patch


class OwnedFixtureTests(unittest.TestCase):
    def test_benchmark_acceptance_includes_mounted_errors(self):
        events = [{"benchmark": {"provisional_acceptance": True}}, {"mounted_benchmark": {"measurements": {"Errors": {}}}}]
        self.assertTrue(benchmark_acceptance(events))
        events[1]["mounted_benchmark"]["measurements"]["Errors"] = {"HTTP 500": 1}
        self.assertFalse(benchmark_acceptance(events))
        self.assertFalse(benchmark_acceptance([]))
        self.assertFalse(benchmark_acceptance([events[0], {"mounted_benchmark_failure": "reset race 500"}]))

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

    def test_adapter_tests_reject_skips_and_restore_environment(self):
        fixture = OwnedFixture()
        try:
            with patch.object(fixture, "run", return_value='{"Action":"skip","Test":"TestCacheSnapshots"}'):
                with self.assertRaises(RuntimeError):
                    run_adapter_cache_tests(fixture)
            self.assertEqual(fixture.env["GOWORK"], "off")
            with patch.object(fixture, "run", return_value='{"Action":"pass","Test":"TestCacheSnapshots"}'):
                run_adapter_cache_tests(fixture)
            self.assertEqual(fixture.events[-1]["passed"], 1)
        finally:
            fixture.cleanup()

    def test_mounted_requires_all_cases_and_cleans_environment(self):
        fixture = OwnedFixture()
        fixture.env["FIXTURE_BACKEND"] = "pgx"
        try:
            for output in ('{"Action":"skip","Test":"TestMounted"}', '{"Action":"pass","Test":"TestMounted"}'):
                with patch.object(fixture, "run", return_value=output):
                    with self.assertRaises(RuntimeError):
                        run_mounted_tests(fixture)
                self.assertNotIn("FIXTURE_MOUNTED", fixture.env)
            output = "\n".join('{"Action":"pass","Test":"' + name + '"}' for name in ("TestMounted/direct", "TestMounted/lru", "TestMounted/redis", "TestMounted"))
            with patch.object(fixture, "run", return_value=output):
                run_mounted_tests(fixture)
            self.assertEqual(fixture.events[-1]["mounted_backend"], "pgx")
        finally:
            fixture.cleanup()

    def test_container_cleanup_requires_owner_label(self):
        fixture = OwnedFixture()
        cidfile = fixture.root / "owned.cid"
        identity = "a" * 64
        cidfile.write_text(identity)
        fixture.container_files.append(cidfile)
        try:
            with patch.object(fixture, "run", return_value='[{"Config":{"Labels":{"authorization-cache-owner":"someone-else"}}}]'):
                with self.assertRaises(RuntimeError):
                    fixture.cleanup()
            self.assertTrue(fixture.root.exists())
            with patch.object(fixture, "run", side_effect=['[{"Config":{"Labels":{"authorization-cache-owner":"' + fixture.token + '"}}}]', ""]) as run:
                fixture.cleanup()
                self.assertEqual(run.call_args_list[-1].args[0], ["docker", "rm", "--force", identity])
        finally:
            if fixture.root.exists():
                fixture.container_files.clear()
                fixture.cleanup()


    def test_live_missing_configuration_refuses_before_startup(self):
        with tempfile.TemporaryDirectory() as root, patch.dict("os.environ", {}, clear=True), patch("authorization_cache_verify.OwnedFixture") as start:
            self.assertEqual(main(["--mode", "firestore-live", "--report", str(Path(root)/"report.json")]), 1)
            start.assert_not_called()

    def test_live_requires_owned_nondefault_target_and_explicit_credentials(self):
        with tempfile.TemporaryDirectory() as root:
            credential = Path(root)/"synthetic.json"
            credential.write_text("not a credential; this test never calls a provider")
            valid = {"FIRESTORE_LIVE_PROJECT_ID":"fixture-project", "FIRESTORE_LIVE_DATABASE_ID":"ci-fixture", "FIRESTORE_LIVE_OWNED":"ci-fixture", "GOOGLE_APPLICATION_CREDENTIALS":str(credential)}
            self.assertEqual(live_environment(valid)["FIRESTORE_LIVE_REQUIRED"], "1")
            for changes in ({"FIRESTORE_LIVE_DATABASE_ID":"(default)"}, {"FIRESTORE_LIVE_OWNED":"other"}, {"GOOGLE_APPLICATION_CREDENTIALS":""}, {"FIRESTORE_EMULATOR_HOST":"127.0.0.1:1234"}):
                with self.assertRaises(ValueError):
                    live_environment({**valid, **changes})



if __name__ == "__main__":
    unittest.main()
