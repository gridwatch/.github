#!/usr/bin/env python3
"""Fail when a `# see` link names a version the module does not allow.

The fleet requires every `resource` and `data` block to carry a `# see`
comment linking to its Terraform Registry documentation, pinned to the
provider's constraint floor. Nothing else checks that: a provider bump moves
`required_providers` and leaves every link behind, still pointing at docs for
a version the workspace no longer allows. `latest` is the same drift with no
visible symptom.

Only the standard library, so CI needs no environment of its own.
"""

from __future__ import annotations

import argparse
import pathlib
import re
import sys

# `# see https://registry.terraform.io/providers/<namespace>/<name>/<version>/docs/...`
# The trailing path is deliberately not captured — this checks the version
# segment, not whether the page exists.
LINK = re.compile(
    r"registry\.terraform\.io/providers/"
    r"(?P<namespace>[^/\s]+)/(?P<name>[^/\s]+)/(?P<version>[^/\s#]+)"
)

# A `required_providers` entry: `tfe = { source = "..." version = "..." }`.
# Provider entries hold no nested braces, so a non-greedy `[^{}]*` is enough
# and avoids depending on an HCL parser.
ENTRY = re.compile(r"(?P<local_name>[A-Za-z0-9_-]+)\s*=\s*\{(?P<body>[^{}]*)\}")
SOURCE = re.compile(r"source\s*=\s*\"(?P<source>[^\"]+)\"")
VERSION = re.compile(r"version\s*=\s*\"(?P<version>[^\"]+)\"")

# The floor of `>= 0.80.0, < 1.0.0`. Only the `>=` bound is the documented
# version; the upper bound is a guard rail, not a thing anyone reads docs for.
FLOOR = re.compile(r">=\s*(?P<floor>\d+\.\d+\.\d+)")


def required_providers(text: str) -> dict[str, str]:
    """Map provider source to constraint floor for one file's declarations."""
    found: dict[str, str] = {}
    for block in brace_blocks(text, "required_providers"):
        for entry in ENTRY.finditer(block):
            body = entry.group("body")
            source = SOURCE.search(body)
            version = VERSION.search(body)
            if not source or not version:
                continue
            floor = FLOOR.search(version.group("version"))
            if floor:
                found[source.group("source").lower()] = floor.group("floor")
    return found


def brace_blocks(text: str, keyword: str) -> list[str]:
    """Return the brace-balanced body of every `keyword { ... }` block."""
    blocks = []
    for match in re.finditer(rf"\b{keyword}\s*\{{", text):
        depth = 0
        start = match.end() - 1
        for index in range(start, len(text)):
            if text[index] == "{":
                depth += 1
            elif text[index] == "}":
                depth -= 1
                if depth == 0:
                    blocks.append(text[start + 1 : index])
                    break
    return blocks


def constraints_for(path: pathlib.Path, by_dir: dict[pathlib.Path, dict[str, str]]) -> dict[str, str]:
    """Resolve the nearest `required_providers` for a file.

    A repo can hold several Terraform roots (`infrastructure-github` has
    `agents/`, `workflows/` and its own root), each pinning independently, so
    the declarations that govern a file are the closest ones at or above it.
    """
    directory = path.parent
    for candidate in [directory, *directory.parents]:
        if candidate in by_dir:
            return by_dir[candidate]
    return {}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("paths", nargs="*", default=["."], help="directories to scan (default: .)")
    parser.add_argument("--fix", action="store_true", help="rewrite stale links instead of reporting them")
    args = parser.parse_args()

    files: list[pathlib.Path] = []
    for root in args.paths:
        for path in sorted(pathlib.Path(root).rglob("*.tf")):
            if ".terraform" not in path.parts:
                files.append(path)

    by_dir: dict[pathlib.Path, dict[str, str]] = {}
    contents: dict[pathlib.Path, str] = {}
    for path in files:
        contents[path] = path.read_text(encoding="utf-8")
        declared = required_providers(contents[path])
        if declared:
            by_dir.setdefault(path.parent, {}).update(declared)

    problems: list[str] = []
    fixed = 0

    for path in files:
        text = contents[path]
        expected = constraints_for(path, by_dir)
        if not expected:
            continue

        replacements: list[tuple[str, str]] = []
        for line_number, line in enumerate(text.splitlines(), start=1):
            for link in LINK.finditer(line):
                source = f"{link.group('namespace')}/{link.group('name')}".lower()
                floor = expected.get(source)
                # A link to a provider this root doesn't declare is a
                # cross-reference, not drift — there is no floor to check it
                # against, so leave it alone.
                if floor is None:
                    continue
                actual = link.group("version")
                if actual == floor:
                    continue

                stale = f"providers/{link.group('namespace')}/{link.group('name')}/{actual}"
                current = f"providers/{link.group('namespace')}/{link.group('name')}/{floor}"
                if args.fix:
                    replacements.append((stale, current))
                else:
                    reason = "floats" if actual == "latest" else f"names {actual}"
                    problems.append(
                        f"{path}:{line_number}: {source} link {reason}, "
                        f"but required_providers pins the floor at {floor}"
                    )

        if replacements:
            for stale, current in replacements:
                text = text.replace(stale, current)
            path.write_text(text, encoding="utf-8")
            fixed += len(replacements)

    if args.fix:
        print(f"rewrote {fixed} link(s)")
        return 0

    if problems:
        print("\n".join(problems), file=sys.stderr)
        print(
            f"\n{len(problems)} `# see` link(s) do not match their provider's constraint floor.\n"
            "Pin each link to the version in `required_providers`, or run with --fix.",
            file=sys.stderr,
        )
        return 1

    print(f"checked {len(files)} file(s): every `# see` link matches its provider's constraint floor")
    return 0


if __name__ == "__main__":
    sys.exit(main())
