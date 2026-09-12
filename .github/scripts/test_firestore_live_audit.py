import tempfile
import unittest
from pathlib import Path

from firestore_live_audit import audit, json_values, live_roots


class LiveAuditTests(unittest.TestCase):
    roots = {("module", "TestSuiteLive")}

    def event(self, action, test="TestSuiteLive", package="module"):
        return {"Action": action, "Test": test, "Package": package}

    def check(self, events, *, roots=None, allowed=None, required=True, status=0, minimum=1):
        return audit(self.roots if roots is None else roots, events, minimum, required, allowed or set(), status)

    def test_live_roots_use_selected_files_and_package_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            Path(directory, "live_test.go").write_text("func TestSuiteLive(t *testing.T) {}\nfunc TestOrdinary(t *testing.T) {}\n")
            Path(directory, "excluded_test.go").write_text("func TestExcludedLive(t *testing.T) {}\n")
            packages = [{"Dir": directory, "ImportPath": name, "TestGoFiles": ["live_test.go"]} for name in ("module", "module/helper")]
            self.assertEqual(live_roots(packages), self.roots | {("module/helper", "TestSuiteLive")})

    def test_success(self):
        self.assertTrue(self.check([self.event("run"), self.event("pass")])[1])

    def test_empty_missing_wrong_package_and_floor_fail(self):
        self.assertFalse(self.check([], roots=set())[1])
        self.assertFalse(self.check([])[1])
        self.assertFalse(self.check([self.event("pass", package="other")])[1])
        self.assertFalse(self.check([self.event("pass")], minimum=2)[1])

    def test_parent_pass_cannot_hide_nested_skip(self):
        self.assertFalse(self.check([self.event("skip", "TestSuiteLive/Credentials"), self.event("pass")])[1])

    def test_only_exact_allowed_skip(self):
        events = [self.event("skip", "TestSuiteLive/Ambient"), self.event("pass")]
        self.assertTrue(self.check(events, allowed={"TestSuiteLive/Ambient"})[1])
        self.assertFalse(self.check(events, allowed={"TestSuiteLive"})[1])
        self.assertFalse(self.check(events, allowed={"TestSuiteLive/Ambient/Child"})[1])

    def test_root_skip_allowed_but_absence_is_not(self):
        self.assertTrue(self.check([self.event("skip")], allowed={"TestSuiteLive"})[1])
        self.assertFalse(self.check([], allowed={"TestSuiteLive"})[1])

    def test_optional_skip_is_warning(self):
        report, passed = self.check([self.event("skip")], required=False)
        self.assertTrue(passed)
        self.assertTrue(any("not release evidence" in line for line in report))

    def test_failure_unfinished_and_exit_status_fail(self):
        self.assertFalse(self.check([self.event("fail"), self.event("pass")])[1])
        self.assertFalse(self.check([self.event("run", "TestSuiteLive/Child"), self.event("pass")])[1])
        self.assertFalse(self.check([self.event("pass"), {"Action": "fail", "Package": "module"}])[1])
        self.assertFalse(self.check([self.event("pass")], status=1)[1])

    def test_malformed_json_is_rejected(self):
        self.assertEqual(list(json_values(' {"Action":"pass"}\n {"Action":"skip"} ')), [{"Action": "pass"}, {"Action": "skip"}])
        with self.assertRaises(ValueError):
            list(json_values('{"Action":"pass"}\ntruncated'))


if __name__ == "__main__":
    unittest.main()
