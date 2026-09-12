import unittest

from firestore_index_readiness import check_composites, check_fields


class IndexReadinessTests(unittest.TestCase):
    composite = [["tuples", "--field-config=field-path=resource_id,order=ascending", "--field-config=field-path=subject_id,order=descending"]]
    field = [["proofs", "payload", "--disable-indexes"]]

    def index(self, state="READY", group="tuples", direction="DESCENDING"):
        return {"name": f"projects/p/databases/d/collectionGroups/{group}/indexes/i", "queryScope": "COLLECTION", "state": state, "fields": [
            {"fieldPath": "resource_id", "order": "ASCENDING"}, {"fieldPath": "subject_id", "order": direction}, {"fieldPath": "__name__", "order": direction},
        ]}

    def deployed_field(self, **config):
        return {"name": "projects/p/databases/d/collectionGroups/proofs/fields/payload", "indexConfig": config}

    def test_exact_composite_ready_with_implicit_name(self):
        self.assertEqual(check_composites(self.composite, [self.index()]), ([], []))

    def test_unrelated_ready_indexes_do_not_cover_missing_or_building_index(self):
        for indexes in ([self.index(group="other")], [self.index(direction="ASCENDING")], [self.index(state="CREATING"), self.index(group="other")]):
            pending, broken = check_composites(self.composite, indexes)
            self.assertEqual(len(pending), 1)
            self.assertFalse(broken)

    def test_repair_failure_is_distinct_from_pending(self):
        pending, broken = check_composites(self.composite, [self.index(state="NEEDS_REPAIR")])
        self.assertFalse(pending)
        self.assertEqual(len(broken), 1)

    def test_disabled_field_must_be_explicit_and_empty(self):
        self.assertEqual(check_fields(self.field, [self.deployed_field(indexes=[])]), ([], []))
        for fields in ([], [self.deployed_field(usesAncestorConfig=True)], [self.deployed_field(reverting=True)]):
            self.assertEqual(len(check_fields(self.field, fields)[0]), 1)

    def test_field_modes_and_states_must_match(self):
        rows = [["proofs", "payload", "--index=order=ascending"]]
        ready = {"order": "ASCENDING", "queryScope": "COLLECTION", "state": "READY"}
        self.assertEqual(check_fields(rows, [self.deployed_field(indexes=[ready])]), ([], []))
        for mode in ({**ready, "state": "CREATING"}, {**ready, "order": "DESCENDING"}, {**ready, "queryScope": "COLLECTION_GROUP"}):
            self.assertEqual(len(check_fields(rows, [self.deployed_field(indexes=[mode])])[0]), 1)
        self.assertEqual(len(check_fields(rows, [self.deployed_field(indexes=[{**ready, "state": "NEEDS_REPAIR"}])])[1]), 1)

    def test_extra_field_mode_is_not_a_match(self):
        actual = self.deployed_field(indexes=[{"order": "ASCENDING", "state": "READY"}])
        self.assertEqual(len(check_fields(self.field, [actual])[0]), 1)


if __name__ == "__main__":
    unittest.main()
