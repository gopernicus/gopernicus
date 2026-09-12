"""Audit Go's live-test JSON, including skipped or unfinished subtests."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess


def json_values(text):
    decoder = json.JSONDecoder()
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        yield value
        text = text.lstrip()[end:]


def live_roots(packages):
    roots = set()
    for package in packages:
        for name in package.get("TestGoFiles", []) + package.get("XTestGoFiles", []):
            source = (Path(package["Dir"]) / name).read_text()
            for test in re.findall(r"^func (Test\w*Live)\s*\(", source, re.MULTILINE):
                roots.add((package["ImportPath"], test))
    return roots


def audit(roots, events, minimum, required, allowed, status):
    messages, failures = [], []
    states, started = {}, set()
    if not roots or len(roots) < minimum:
        failures.append(f"derived {len(roots)} live roots; expected at least {minimum}")
    for event in events:
        action = event.get("Action")
        test = event.get("Test")
        package = event.get("Package", "")
        if not test:
            if action == "fail":
                failures.append(f"package failed: {package}")
            continue
        key = (package, test)
        if action == "run":
            started.add(key)
        if action in {"pass", "fail", "skip"}:
            states[key] = action
            if action == "fail":
                failures.append(f"test failed: {package}/{test}")
    for key in sorted(roots):
        state = states.get(key, "ABSENT")
        messages.append(f"{key[0]}/{key[1]}: {state}")
        if state == "ABSENT":
            failures.append(f"live root never completed: {key[0]}/{key[1]}")
    for package, test in sorted(started - states.keys()):
        failures.append(f"test never completed: {package}/{test}")
    # Exact names only: allowing one known family must not allow its descendants
    # or a similarly named test. Stale allowances fail instead of accumulating.
    for test in sorted(allowed):
        matches = [key for key in states if key[1] == test and (key[0], test.split('/')[0]) in roots]
        if len(matches) != 1:
            failures.append(f"allowed skip is absent or ambiguous: {test}")
    for (package, test), state in sorted(states.items()):
        if state != "skip":
            continue
        if test in allowed and (package, test.split('/')[0]) in roots:
            messages.append(f"ALLOWED SKIP: {package}/{test}")
        elif required:
            failures.append(f"unexpected skip: {package}/{test}")
        else:
            messages.append(f"WARNING: skipped {package}/{test}; this is not release evidence")
    if status != 0:
        failures.append(f"go test exited {status}")
    return messages + [f"ERROR: {failure}" for failure in failures], not failures


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--prefix", required=True, help="JSON input and evidence path prefix")
    parser.add_argument("--minimum", required=True, type=int)
    parser.add_argument("--status", required=True, type=int)
    parser.add_argument("--allow-skip", action="append", default=[])
    args = parser.parse_args()
    prefix = Path(args.prefix)
    allowed = set(args.allow_skip)
    Path(f"{prefix}-allowed.txt").write_text("".join(f"{name}\n" for name in sorted(allowed)))
    try:
        listing = subprocess.run(
            ["go", "list", "-json", "-tags=integration,live", "./..."],
            check=True, text=True, capture_output=True,
        )
        roots = live_roots(json_values(listing.stdout))
        Path(f"{prefix}-expected.txt").write_text("".join(f"{pkg}/{test}\n" for pkg, test in sorted(roots)))
        events = list(json_values(Path(f"{prefix}.json").read_text()))
        messages, passed = audit(roots, events, args.minimum, os.getenv("FIRESTORE_LIVE_REQUIRED") == "1", allowed, args.status)
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        messages, passed = [f"ERROR: live-test evidence could not be audited: {error}"], False
    report = "\n".join(messages) + "\n"
    Path(f"{prefix}-audit.txt").write_text(report)
    print(report, end="")
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
