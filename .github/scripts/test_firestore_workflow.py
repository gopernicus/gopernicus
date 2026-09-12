import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


def step_script(name):
    workflow = (ROOT / ".github/workflows/live-stores.yml").read_text()
    section = workflow.split(f"      - name: {name}\n", 1)[1]
    lines = section.split("        run: |\n", 1)[1].splitlines()
    script = []
    for line in lines:
        if line and not line.startswith("          "):
            break
        script.append(line[10:])
    return "\n".join(script)


class WorkflowTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name)
        self.env_file = self.path / "env"
        self.calls_file = self.path / "calls"
        self.env_file.touch()
        self.calls_file.touch()
        gcloud = self.path / "gcloud"
        gcloud.write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$FAKE_CALLS"\nexit "${FAKE_STATUS:-0}"\n')
        gcloud.chmod(0o700)
        self.env = {
            **os.environ,
            "PATH": f"{self.path}:{os.environ['PATH']}",
            "GITHUB_ENV": str(self.env_file),
            "GITHUB_RUN_ID": "12345",
            "GITHUB_RUN_ATTEMPT": "2",
            "FIRESTORE_LIVE_PROJECT_ID": "test-project",
            "FIRESTORE_LIVE_DATABASE_PIN": "",
            "FIRESTORE_LIVE_LOCATION": "test-region",
            "FAKE_CALLS": str(self.calls_file),
            "RUNNER_TEMP": str(self.path),
        }

    def run_step(self, name, **environment):
        return subprocess.run(["bash", "-e", "-c", step_script(name)], cwd=ROOT, env={**self.env, **environment}, text=True, capture_output=True)

    def test_required_configuration_cannot_skip(self):
        result = self.run_step("check the live Firestore configuration", FIRESTORE_LIVE_PROJECT_ID="", HAVE_CREDENTIALS="", FIRESTORE_LIVE_REQUIRED="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("LIVE_CONFIGURED=1", self.env_file.read_text())

    def test_optional_missing_configuration_is_explicit(self):
        result = self.run_step("check the live Firestore configuration", HAVE_CREDENTIALS="", FIRESTORE_LIVE_REQUIRED="")
        self.assertEqual(result.returncode, 0)
        self.assertIn("NOT verified", result.stdout)

    def test_required_run_refuses_pinned_database_even_with_credentials(self):
        result = self.run_step("check the live Firestore configuration", HAVE_CREDENTIALS="1", FIRESTORE_LIVE_REQUIRED="1", FIRESTORE_LIVE_DATABASE_PIN="dedicated-test")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("disposable run-owned database", result.stdout)
        self.assertNotIn("LIVE_CONFIGURED=1", self.env_file.read_text())
        self.assertEqual(self.calls_file.read_text(), "")

    def test_default_pin_is_refused_before_any_gcloud_call(self):
        result = self.run_step("provision a disposable Firestore database", FIRESTORE_LIVE_DATABASE_PIN="(default)")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls_file.read_text(), "")
        self.assertEqual(self.env_file.read_text(), "")

    def test_pinned_database_is_never_owned(self):
        result = self.run_step("provision a disposable Firestore database", FIRESTORE_LIVE_DATABASE_PIN="dedicated-test")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("FIRESTORE_LIVE_DATABASE_ID=dedicated-test", self.env_file.read_text())
        self.assertNotIn("FIRESTORE_LIVE_OWNED", self.env_file.read_text())
        self.assertEqual(self.calls_file.read_text(), "")

    def test_failed_create_does_not_claim_ownership(self):
        result = self.run_step("provision a disposable Firestore database", FAKE_STATUS="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("FIRESTORE_LIVE_DATABASE_CANDIDATE=ci-12345-2-", self.env_file.read_text())
        self.assertNotIn("FIRESTORE_LIVE_OWNED", self.env_file.read_text())
        self.assertNotIn("FIRESTORE_LIVE_DATABASE_ID", self.env_file.read_text())

    def test_successful_create_records_new_owned_database_each_time(self):
        names = set()
        for _ in range(2):
            self.env_file.write_text("")
            result = self.run_step("provision a disposable Firestore database")
            self.assertEqual(result.returncode, 0, result.stderr)
            values = dict(line.split("=", 1) for line in self.env_file.read_text().splitlines())
            self.assertEqual(values["FIRESTORE_LIVE_OWNED"], values["FIRESTORE_LIVE_DATABASE_ID"])
            names.add(values["FIRESTORE_LIVE_OWNED"])
        self.assertEqual(len(names), 2)

    def test_cleanup_requires_confirmed_matching_run_database(self):
        for owned, database in [("(default)", "(default)"), ("ci-1", "ci-2"), ("dedicated-test", "dedicated-test")]:
            result = self.run_step("delete the run-owned Firestore database", FIRESTORE_LIVE_OWNED=owned, FIRESTORE_LIVE_DATABASE_ID=database)
            self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls_file.read_text(), "")

    def test_cleanup_failure_is_not_suppressed(self):
        result = self.run_step("delete the run-owned Firestore database", FIRESTORE_LIVE_OWNED="ci-12345-2-owned", FIRESTORE_LIVE_DATABASE_ID="ci-12345-2-owned", FAKE_STATUS="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("databases delete", self.calls_file.read_text())

    def test_deploy_submits_async_composites_and_both_field_modes_then_waits(self):
        manifest = {
            "indexes": [{"collectionGroup": "docs", "queryScope": "COLLECTION", "fields": [{"fieldPath": "a", "order": "ASCENDING"}, {"fieldPath": "b", "order": "DESCENDING"}]}],
            "fieldOverrides": [
                {"collectionGroup": "docs", "fieldPath": "payload", "indexes": []},
                {"collectionGroup": "docs", "fieldPath": "a", "indexes": [{"order": "ASCENDING", "queryScope": "COLLECTION"}]},
            ],
        }
        manifest_path = self.path / "manifest.json"
        manifest_path.write_text(json.dumps(manifest))
        indexes = manifest["indexes"][:]
        for direction in ["ASCENDING", "DESCENDING"]:
            for field in ["created_at", "n"]:
                indexes.append({"collectionGroup": "firestore_c4_list_live", "queryScope": "COLLECTION", "fields": [
                    {"fieldPath": "group", "order": "ASCENDING"}, {"fieldPath": field, "order": direction}, {"fieldPath": "id", "order": direction},
                ]})
        for index in indexes:
            index["name"] = f"projects/p/databases/d/collectionGroups/{index['collectionGroup']}/indexes/i"
            index["state"] = "READY"
        fields = [{"name": f"projects/p/databases/d/collectionGroups/docs/fields/{override['fieldPath']}", "indexConfig": {"indexes": [{**index, "state": "READY"} for index in override["indexes"]]}} for override in manifest["fieldOverrides"]]
        (self.path / "indexes.json").write_text(json.dumps(indexes))
        (self.path / "fields.json").write_text(json.dumps(fields))
        (self.path / "gcloud").write_text('''#!/bin/bash
printf "%s\\n" "$*" >> "$FAKE_CALLS"
if [[ "$*" == "firestore indexes composite list "* ]]; then cat "$RUNNER_TEMP/indexes.json"; fi
if [[ "$*" == "firestore indexes fields list "* ]]; then cat "$RUNNER_TEMP/fields.json"; fi
exit 0
''')
        result = self.run_step("deploy the index manifests (composites + field configuration) and wait", FIRESTORE_LIVE_INDEXES=str(manifest_path), FIRESTORE_LIVE_DATABASE_ID="ci-test")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        calls = self.calls_file.read_text()
        self.assertEqual(calls.count("composite create"), 5)
        self.assertEqual(calls.count("fields update"), 2)
        self.assertEqual(calls.count("--async"), 7)
        self.assertIn("--disable-indexes", calls)
        self.assertIn("--index=order=ascending", calls)
        self.assertIn("FIRESTORE_INDEXES_READY=1", self.env_file.read_text())


if __name__ == "__main__":
    unittest.main()
