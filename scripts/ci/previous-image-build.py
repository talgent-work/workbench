#!/usr/bin/env python3
"""Read the source commit recorded by the last successful build of this branch."""
import io
import json
import os
import re
import subprocess
import urllib.parse
import zipfile


def api(path):
    return subprocess.check_output(["gh", "api", path])


def previous_build(repo, branch):
    artifact_name = "branch-image-source"
    page = 1
    while True:
        query = urllib.parse.urlencode({"status": "success", "branch": branch,
                                        "per_page": 100, "page": page})
        runs = json.loads(api(f"repos/{repo}/actions/workflows/ci.yml/runs?{query}"))["workflow_runs"]
        for run in runs:
            if run["event"] not in ("push", "workflow_dispatch"):
                continue
            artifacts = json.loads(api(f"repos/{repo}/actions/runs/{run['id']}/artifacts"))["artifacts"]
            for artifact in artifacts:
                if artifact["name"] != artifact_name or artifact["expired"]:
                    continue
                with zipfile.ZipFile(io.BytesIO(api(f"repos/{repo}/actions/artifacts/{artifact['id']}/zip"))) as archive:
                    commit = archive.read("source.txt").decode().strip()
                if not re.fullmatch(r"[0-9a-f]{40}", commit):
                    raise ValueError("Invalid image build source commit")
                return commit
        if len(runs) < 100:
            return ""
        page += 1


if __name__ == "__main__":
    source = previous_build(os.environ["GITHUB_REPOSITORY"], os.environ["HEAD_BRANCH"])
    print(f"Previous successful branch image source: {source or 'none; use branch fork point'}")
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.write(f"base_ref={source}\n")
