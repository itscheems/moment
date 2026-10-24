#!/usr/bin/env python3
"""Safety and interrupted-run coverage for the additive comment fixture."""

import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("comment_showcase", Path(__file__).with_name("comment-showcase.py"))
SHOWCASE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SHOWCASE)


class ShowcaseTest(unittest.TestCase):
    def test_refuses_production_wrong_instance_and_non_showcase(self):
        valid = {"environment": "development", "instance_id": "test"}
        seed = {"plan": {"scenario": "site-showcase"}}
        site = {"meta": {"instance_id": "test"}}
        SHOWCASE.assert_playground(valid, seed, site)
        for instance, recipe, observed in [
            ({**valid, "environment": "production"}, seed, site),
            (valid, seed, {"meta": {"instance_id": "other"}}),
            (valid, {"plan": {"scenario": "other"}}, site),
            ({}, seed, site),
        ]:
            with self.assertRaises(SystemExit):
                SHOWCASE.assert_playground(instance, recipe, observed)

    def test_resumes_without_creating_duplicates_and_keeps_retry_key(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "manifest.json"
            calls = []
            fail = [False]

            def runner(actor, *args):
                calls.append((actor, args))
                if fail[0]:
                    raise SystemExit("simulated failure")
                return {"id": "id-" + str(len(calls))}

            first = SHOWCASE.Builder(path, "test", runner)
            created = first.entity("root", "aming", "comment", "create", "post")
            fail[0] = True
            with self.assertRaises(SystemExit):
                first.entity("reply", "lena", "comment", "reply", "post", created["id"])
            retry_key = calls[-1][1][-1]
            resumed = SHOWCASE.Builder(path, "test", runner)
            self.assertEqual(resumed.entity("root", "aming", "comment", "create", "post"), created)
            self.assertEqual(len(calls), 2)
            fail[0] = False
            resumed.entity("reply", "lena", "comment", "reply", "post", created["id"])
            self.assertEqual(calls[-1][1][-1], retry_key)
            resumed.case("root", "post", created["id"])
            resumed.case("root", "post", created["id"])
            self.assertEqual(len(resumed.manifest["cases"]), 1)

    def test_existing_manifest_never_crosses_instances(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "manifest.json"
            original = SHOWCASE.Builder(path, "first")
            original.manifest["entities"]["root"] = {"id": "old"}
            original.save()
            other = SHOWCASE.Builder(path, "second")
            self.assertEqual(other.manifest["entities"], {})
            self.assertNotEqual(original.manifest["run_id"], other.manifest["run_id"])


if __name__ == "__main__":
    unittest.main()
