#!/usr/bin/env python3
"""The real resolver chooses only retained successful-build artifacts for this branch."""
import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("baseline", ROOT / "scripts/ci/previous-image-build.py")
baseline = importlib.util.module_from_spec(spec)
spec.loader.exec_module(baseline)


class PreviousBuildTest(unittest.TestCase):
    def archive(self, value):
        data = io.BytesIO()
        with zipfile.ZipFile(data, "w") as out:
            out.writestr("source.txt", value)
        return data.getvalue()

    def test_latest_completed_success_with_matching_artifact(self):
        paths = []
        responses = [
            {"workflow_runs": [{"id": 5, "event": "push"}, {"id": 4, "event": "push"}, {"id": 3, "event": "push"}, {"id": 2, "event": "workflow_dispatch"}]},
            {"artifacts": []},  # Old workflow did not record image completion.
            {"artifacts": [{"id": 40, "name": "pr-99-image-source", "expired": False}]},
            {"artifacts": [{"id": 30, "name": "branch-image-source", "expired": True}]},
            {"artifacts": [{"id": 20, "name": "branch-image-source", "expired": False}]},
            self.archive("a" * 40),
        ]

        def api(path):
            paths.append(path)
            response = responses.pop(0)
            return response if isinstance(response, bytes) else json.dumps(response).encode()

        with patch.object(baseline, "api", side_effect=api):
            self.assertEqual(baseline.previous_build("owner/repo", "fix/board"), "a" * 40)
        self.assertIn("status=success", paths[0])
        self.assertNotIn("event=", paths[0])  # Include manual builds as well as pushes.
        self.assertIn("branch=fix%2Fboard", paths[0])
        self.assertFalse(responses)

    def test_first_build_has_no_previous_source(self):
        with patch.object(baseline, "api", return_value=b'{"workflow_runs": []}'):
            self.assertEqual(baseline.previous_build("owner/repo", "fix/board"), "")

    def test_api_failure_does_not_silently_choose_a_base(self):
        with patch.object(baseline, "api", side_effect=RuntimeError("API unavailable")):
            with self.assertRaises(RuntimeError):
                baseline.previous_build("owner/repo", "fix/board")

    def test_invalid_artifact_is_rejected(self):
        responses = [b'{"workflow_runs": [{"id": 1, "event": "push"}]}',
                     b'{"artifacts": [{"id": 1, "name": "branch-image-source", "expired": false}]}',
                     self.archive("HEAD; echo injected")]
        with patch.object(baseline, "api", side_effect=responses):
            with self.assertRaises(ValueError):
                baseline.previous_build("owner/repo", "fix/board")


if __name__ == "__main__":
    unittest.main()
