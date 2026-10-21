"""Select required CI jobs; unknown inputs conservatively select every job."""

import os
import subprocess
from pathlib import Path

JOBS = ("go", "web", "packaging")
GO_INPUTS = ("cmd/", "internal/", "benchmarks/", "web/dist/")


def select_jobs(paths):
    selected = set()
    for path in paths:
        if path.endswith(".md") or path.startswith("docs/") or path == "index.html":
            continue
        elif path in ("go.mod", "go.sum", "web/embed.go") or path.startswith(GO_INPUTS):
            selected.update(("go", "packaging"))
        elif path.startswith("web/"):
            selected.update(("web", "packaging"))
        elif path.startswith("deploy/"):
            selected.add("packaging")
        else:
            return set(JOBS)
    return selected


def main():
    selected = set(JOBS)
    if os.environ.get("EVENT") == "pull_request":
        try:
            # Disable rename detection so moving code into docs still tests its deletion.
            diff = subprocess.check_output([
                "git", "diff", "--name-only", "--no-renames", "-z",
                f'{os.environ["BASE_SHA"]}...{os.environ["HEAD_SHA"]}',
            ])
            selected = select_jobs(os.fsdecode(diff).rstrip("\0").split("\0"))
        except (OSError, subprocess.CalledProcessError, KeyError) as error:
            print(f"Change detection failed; running all checks: {error}")
    output = "".join(f"{job}={str(job in selected).lower()}\n" for job in JOBS)
    print(output, end="")
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as stream:
        stream.write(output)


if __name__ == "__main__":
    main()
