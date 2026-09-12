"""Match deployed Firestore index state to the exact CLI deployment specs."""

import argparse
import csv
import json
from pathlib import Path


def fields_key(fields):
    return tuple((field["fieldPath"], field.get("order", ""), field.get("arrayConfig", "")) for field in fields)


def modes_key(indexes):
    return frozenset((index.get("queryScope", "COLLECTION"), index.get("order", ""), index.get("arrayConfig", "")) for index in indexes)


def parameters(flag):
    return dict(part.split("=", 1) for part in flag.split("=", 1)[1].split(","))


def composite_spec(row):
    group, *flags = row
    scope, fields = "COLLECTION", []
    for flag in flags:
        if flag.startswith("--query-scope="):
            scope = flag.split("=", 1)[1].upper().replace("-", "_")
        else:
            values = parameters(flag)
            field = {"fieldPath": values["field-path"]}
            for source, target in [("order", "order"), ("array-config", "arrayConfig")]:
                if source in values:
                    field[target] = values[source].upper()
            fields.append(field)
    return group, scope, fields_key(fields)


def resource_key(name, kind):
    suffix = name.split("/collectionGroups/", 1)[1]
    return tuple(suffix.split(f"/{kind}/", 1))


def check_composites(rows, deployed):
    pending, broken = [], []
    for group, scope, fields in map(composite_spec, rows):
        states = []
        for index in deployed:
            if resource_key(index["name"], "indexes")[0] != group or index.get("queryScope") != scope:
                continue
            actual = fields_key(index["fields"])
            # Firestore appends an implicit document-name ordering field.
            if actual != fields and actual and actual[-1][0] == "__name__":
                actual = actual[:-1]
            if actual == fields:
                states.append(index.get("state", "UNKNOWN"))
        label = f"{group} {scope} {fields}"
        if "READY" in states:
            continue
        if "NEEDS_REPAIR" in states:
            broken.append(label)
        else:
            pending.append(f"{label}: {','.join(states) or 'ABSENT'}")
    return pending, broken


def check_fields(rows, deployed):
    pending, broken = [], []
    actual = {resource_key(field["name"], "fields"): field for field in deployed}
    for group, path, *flags in rows:
        expected = []
        for flag in flags:
            if flag == "--disable-indexes":
                continue
            values = parameters(flag)
            expected.append({"queryScope": "COLLECTION", "order": values.get("order", "").upper(), "arrayConfig": values.get("array-config", "").upper()})
        field = actual.get((group, path), {})
        config = field.get("indexConfig")
        label = f"{group}.{path}"
        if config is None or config.get("usesAncestorConfig") or config.get("reverting"):
            pending.append(f"{label}: absent, inherited or reverting")
            continue
        indexes = config.get("indexes", [])
        if any(index.get("state") == "NEEDS_REPAIR" for index in indexes):
            broken.append(label)
        elif modes_key(indexes) != modes_key(expected) or any(index.get("state") != "READY" for index in indexes):
            pending.append(f"{label}: requested modes are not READY")
    return pending, broken


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=["composites", "fields"])
    parser.add_argument("specs")
    parser.add_argument("deployed")
    args = parser.parse_args()
    try:
        rows = list(csv.reader(Path(args.specs).read_text().splitlines(), delimiter="\t"))
        deployed = json.loads(Path(args.deployed).read_text())
        check = check_composites if args.kind == "composites" else check_fields
        pending, broken = check(rows, deployed)
    except (OSError, ValueError, KeyError, IndexError, TypeError) as error:
        print(f"ERROR: index readiness could not be checked: {error}")
        return 2
    for message in broken:
        print(f"ERROR: index needs repair: {message}")
    for message in pending:
        print(f"PENDING: {message}")
    print(f"{len(rows) - len(pending) - len(broken)}/{len(rows)} expected {args.kind} READY")
    return 2 if broken else 1 if pending else 0


if __name__ == "__main__":
    raise SystemExit(main())
