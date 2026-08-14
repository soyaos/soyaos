#!/usr/bin/env python3
"""Plan and publish independently versioned Go modules in this repository.

The root release-please manifest remains the source of truth for the SoyaOS
binary release. This tool owns only subdirectory Go module versions and their
namespaced tags (for example pkg/soyapack/v0.1.0-alpha.2).
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_MANIFEST = ROOT / ".github" / "module-versions.json"
INTERNAL_PREFIX = "github.com/soyaos/soyaos/"
SEMVER_RE = re.compile(
    r"^v(?P<major>0|[1-9][0-9]*)\."
    r"(?P<minor>0|[1-9][0-9]*)\."
    r"(?P<patch>0|[1-9][0-9]*)"
    r"(?:-(?P<prerelease>[0-9A-Za-z.-]+))?$"
)


class ReleaseError(RuntimeError):
    """A release invariant was violated."""


@dataclass(frozen=True)
class Module:
    directory: str
    path: str
    dependencies: frozenset[str]

    @property
    def tag_prefix(self) -> str:
        return f"{self.directory}/"


def run(
    args: list[str],
    *,
    cwd: Path = ROOT,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        args,
        cwd=cwd,
        check=check,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


def git(*args: str, check: bool = True) -> str:
    return run(["git", *args], check=check).stdout.strip()


def load_manifest(path: Path = DEFAULT_MANIFEST) -> dict[str, str]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ReleaseError(f"cannot read module manifest {path}: {exc}") from exc
    if payload.get("schema") != 1 or not isinstance(payload.get("modules"), dict):
        raise ReleaseError("module manifest must contain schema=1 and modules")
    versions = payload["modules"]
    for directory, version in versions.items():
        if not isinstance(directory, str) or not isinstance(version, str):
            raise ReleaseError("module manifest keys and versions must be strings")
        parse_version(version)
    return dict(versions)


def manifest_at(ref: str, relative_path: str = ".github/module-versions.json") -> dict[str, str] | None:
    result = run(["git", "show", f"{ref}:{relative_path}"], check=False)
    if result.returncode != 0:
        return None
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise ReleaseError(f"invalid module manifest at {ref}: {exc}") from exc
    if payload.get("schema") != 1 or not isinstance(payload.get("modules"), dict):
        raise ReleaseError(f"invalid module manifest schema at {ref}")
    return dict(payload["modules"])


def write_manifest(versions: dict[str, str], path: Path = DEFAULT_MANIFEST) -> None:
    payload = {"schema": 1, "modules": dict(sorted(versions.items()))}
    path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")


def discover_modules() -> dict[str, Module]:
    workspace = json.loads(run(["go", "work", "edit", "-json"]).stdout)
    modules: dict[str, Module] = {}
    path_to_directory: dict[str, str] = {}
    raw: dict[str, dict] = {}

    for use in workspace.get("Use") or []:
        directory = Path(use["DiskPath"]).as_posix().removeprefix("./")
        metadata = json.loads(
            run(["go", "mod", "edit", "-json", f"{directory}/go.mod"]).stdout
        )
        module_path = metadata["Module"]["Path"]
        if module_path in path_to_directory:
            raise ReleaseError(f"duplicate module path: {module_path}")
        path_to_directory[module_path] = directory
        raw[directory] = metadata

    for directory, metadata in raw.items():
        module_path = metadata["Module"]["Path"]
        dependencies = frozenset(
            path_to_directory[require["Path"]]
            for require in metadata.get("Require") or []
            if require["Path"] in path_to_directory
        )
        modules[directory] = Module(directory, module_path, dependencies)
    return modules


def parse_version(version: str) -> tuple[int, int, int, str | None]:
    match = SEMVER_RE.fullmatch(version)
    if not match:
        raise ReleaseError(f"unsupported semantic version: {version}")
    return (
        int(match.group("major")),
        int(match.group("minor")),
        int(match.group("patch")),
        match.group("prerelease"),
    )


def version_key(version: str) -> tuple:
    major, minor, patch, prerelease = parse_version(version)
    if prerelease is None:
        return major, minor, patch, 1, ()
    identifiers = tuple(
        (0, int(part)) if part.isdigit() else (1, part)
        for part in prerelease.split(".")
    )
    return major, minor, patch, 0, identifiers


def bump_version(version: str, level: str = "patch") -> str:
    major, minor, patch, prerelease = parse_version(version)
    if prerelease is not None:
        parts = prerelease.split(".")
        if parts[-1].isdigit():
            parts[-1] = str(int(parts[-1]) + 1)
        else:
            parts.append("1")
        return f"v{major}.{minor}.{patch}-" + ".".join(parts)
    if level == "major":
        return f"v{major + 1}.0.0"
    if level == "minor":
        return f"v{major}.{minor + 1}.0"
    if level == "patch":
        return f"v{major}.{minor}.{patch + 1}"
    raise ReleaseError(f"unknown bump level: {level}")


def bump_level_from_commits(tag: str, head_ref: str, directory: str) -> str:
    log = git("log", "--format=%s%n%b%x00", f"{tag}..{head_ref}", "--", directory)
    if re.search(r"(^|\n)[a-zA-Z0-9_-]+(?:\([^)]*\))?!:", log) or "BREAKING CHANGE:" in log:
        return "major"
    if re.search(r"(^|\n)feat(?:\([^)]*\))?:", log):
        return "minor"
    return "patch"


def topological_order(modules: dict[str, Module], selected: Iterable[str]) -> list[str]:
    selected_set = set(selected)
    pending = {
        directory: set(modules[directory].dependencies) & selected_set
        for directory in selected_set
    }
    result: list[str] = []
    while pending:
        ready = sorted(directory for directory, deps in pending.items() if not deps)
        if not ready:
            cycle = ", ".join(sorted(pending))
            raise ReleaseError(f"internal module dependency cycle: {cycle}")
        result.extend(ready)
        for directory in ready:
            del pending[directory]
        for deps in pending.values():
            deps.difference_update(ready)
    return result


def reverse_dependency_closure(
    modules: dict[str, Module], direct: Iterable[str]
) -> tuple[set[str], dict[str, set[str]]]:
    reverse: dict[str, set[str]] = {directory: set() for directory in modules}
    for directory, module in modules.items():
        for dependency in module.dependencies:
            reverse[dependency].add(directory)

    affected = set(direct)
    reasons: dict[str, set[str]] = {directory: {"source changed"} for directory in affected}
    queue = sorted(affected)
    while queue:
        dependency = queue.pop(0)
        for dependent in sorted(reverse[dependency]):
            reasons.setdefault(dependent, set()).add(f"depends on {dependency}")
            if dependent not in affected:
                affected.add(dependent)
                queue.append(dependent)
    return affected, reasons


def tag_for(directory: str, version: str) -> str:
    return f"{directory}/{version}"


def tag_commit(tag: str) -> str | None:
    result = run(["git", "rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}"], check=False)
    if result.returncode != 0:
        return None
    return result.stdout.strip()


def changed_module_files(tag: str, head_ref: str, directory: str) -> list[str]:
    output = git("diff", "--name-only", f"{tag}..{head_ref}", "--", directory)
    return [
        path
        for path in output.splitlines()
        if path and Path(path).name != "go.sum"
    ]


def validate_inventory(modules: dict[str, Module], versions: dict[str, str]) -> None:
    discovered = set(modules)
    declared = set(versions)
    if discovered != declared:
        missing = sorted(discovered - declared)
        stale = sorted(declared - discovered)
        raise ReleaseError(f"module inventory drift; missing={missing}, stale={stale}")
    for directory, version in versions.items():
        tag = tag_for(directory, version)
        if tag_commit(tag) is None:
            raise ReleaseError(f"manifest baseline tag does not exist locally: {tag}")


def build_plan(head_ref: str = "HEAD") -> dict:
    modules = discover_modules()
    versions = load_manifest()
    validate_inventory(modules, versions)

    direct: list[str] = []
    files: dict[str, list[str]] = {}
    for directory in sorted(modules):
        baseline_tag = tag_for(directory, versions[directory])
        changed = changed_module_files(baseline_tag, head_ref, directory)
        if changed:
            direct.append(directory)
            files[directory] = changed

    affected, reasons = reverse_dependency_closure(modules, direct)
    order = topological_order(modules, affected)
    next_versions: dict[str, str] = {}
    for directory in order:
        level = (
            bump_level_from_commits(tag_for(directory, versions[directory]), head_ref, directory)
            if directory in direct
            else "patch"
        )
        next_versions[directory] = bump_version(versions[directory], level)

    return {
        "head": git("rev-parse", head_ref),
        "direct": direct,
        "order": order,
        "modules": [
            {
                "directory": directory,
                "module": modules[directory].path,
                "current": versions[directory],
                "next": next_versions[directory],
                "tag": tag_for(directory, next_versions[directory]),
                "direct": directory in direct,
                "reasons": sorted(reasons.get(directory, set())),
                "files": files.get(directory, []),
            }
            for directory in order
        ],
    }


def apply_plan(plan: dict) -> None:
    modules = discover_modules()
    versions = load_manifest()
    target_versions = {item["directory"]: item["next"] for item in plan["modules"]}

    for directory in plan["order"]:
        module = modules[directory]
        for dependency in sorted(module.dependencies & target_versions.keys()):
            dependency_path = modules[dependency].path
            run(
                [
                    "go",
                    "mod",
                    "edit",
                    f"-require={dependency_path}@{target_versions[dependency]}",
                    f"{directory}/go.mod",
                ]
            )
        versions[directory] = target_versions[directory]
    write_manifest(versions)


def manifest_changes(base_ref: str, head_ref: str = "HEAD") -> dict:
    current = load_manifest()
    previous = manifest_at(base_ref)
    if previous is None:
        return {"bootstrap": True, "base": base_ref, "head": git("rev-parse", head_ref), "modules": [], "order": []}

    modules = discover_modules()
    if set(current) != set(modules):
        raise ReleaseError("current module manifest does not match go.work inventory")
    changed = sorted(
        directory for directory, version in current.items() if previous.get(directory) != version
    )
    removed = sorted(set(previous) - set(current))
    if removed:
        raise ReleaseError(f"module manifest removed entries: {removed}")
    order = topological_order(modules, changed)
    items = []
    for directory in order:
        old = previous.get(directory)
        new = current[directory]
        if old is None:
            raise ReleaseError(f"new modules require an explicit bootstrap workflow: {directory}")
        if version_key(new) <= version_key(old):
            raise ReleaseError(f"module version must increase: {directory} {old} -> {new}")
        items.append(
            {
                "directory": directory,
                "module": modules[directory].path,
                "current": old,
                "next": new,
                "tag": tag_for(directory, new),
            }
        )
    return {
        "bootstrap": False,
        "base": git("rev-parse", base_ref),
        "head": git("rev-parse", head_ref),
        "modules": items,
        "order": order,
    }


def validate_pending_release(changes: dict) -> None:
    modules = discover_modules()
    changed_versions = {item["directory"]: item["next"] for item in changes["modules"]}
    if not changed_versions:
        return

    affected, _ = reverse_dependency_closure(modules, changed_versions)
    if affected != set(changed_versions):
        missing = sorted(affected - set(changed_versions))
        raise ReleaseError(f"release manifest omits reverse dependents: {missing}")

    path_to_directory = {module.path: directory for directory, module in modules.items()}
    for directory in changed_versions:
        metadata = json.loads(
            run(["go", "mod", "edit", "-json", f"{directory}/go.mod"]).stdout
        )
        requires = {
            require["Path"]: require["Version"]
            for require in metadata.get("Require") or []
        }
        for dependency in modules[directory].dependencies & changed_versions.keys():
            dependency_path = modules[dependency].path
            actual = requires.get(dependency_path)
            expected = changed_versions[dependency]
            if actual != expected:
                raise ReleaseError(
                    f"{directory} requires {dependency_path}@{actual}; expected {expected}"
                )
        for replace in metadata.get("Replace") or []:
            old_path = replace["Old"]["Path"]
            if old_path in path_to_directory:
                raise ReleaseError(f"local internal replace is forbidden: {directory}: {old_path}")


def create_local_tags(changes: dict, target_ref: str = "HEAD") -> None:
    validate_pending_release(changes)
    target = git("rev-parse", target_ref)
    for item in changes["modules"]:
        existing = tag_commit(item["tag"])
        if existing is not None and existing != target:
            raise ReleaseError(
                f"refusing to move existing tag {item['tag']}: {existing} != {target}"
            )
        run(["git", "tag", "-f", item["tag"], target])
        print(f"local {item['tag']} -> {target}")


def publish_tags(changes: dict, target_ref: str, remote: str, push: bool) -> None:
    validate_pending_release(changes)
    target = git("rev-parse", target_ref)
    run(["git", "fetch", remote, "--tags", "--force"])
    new_tags: list[str] = []
    for item in changes["modules"]:
        tag = item["tag"]
        existing = tag_commit(tag)
        if existing is not None:
            if existing != target:
                raise ReleaseError(f"immutable tag conflict: {tag} -> {existing}, expected {target}")
            print(f"existing {tag} -> {target}")
            continue
        run(["git", "tag", "-a", tag, target, "-m", f"Release {item['module']} {item['next']}"])
        new_tags.append(tag)
        print(f"create {tag} -> {target}")
    if push and new_tags:
        run(["git", "push", "--atomic", remote, *[f"refs/tags/{tag}" for tag in new_tags]])
        print(f"pushed {len(new_tags)} module tag(s) atomically")
    elif not new_tags:
        print("all module tags already exist at the expected commit")


def render_plan(plan: dict) -> str:
    if not plan["modules"]:
        return "No Go module release is required."
    lines = [
        "## Go module release plan",
        "",
        "This PR was generated from the module dependency graph. Merging it publishes the listed namespaced tags.",
        "",
        "| Module | Current | Next | Reason |",
        "|---|---:|---:|---|",
    ]
    for item in plan["modules"]:
        reason = "; ".join(item["reasons"])
        lines.append(
            f"| `{item['directory']}` | `{item['current']}` | `{item['next']}` | {reason} |"
        )
    lines.extend(
        [
            "",
            "### Automated checks",
            "",
            "- internal `require` versions follow dependency order",
            "- pending tags are simulated locally for `GOWORK=off` tidy/tests",
            "- publishing is atomic and rejects immutable tag conflicts",
            "- the root `vX.Y.Z` release pipeline is not modified by this PR",
        ]
    )
    return "\n".join(lines) + "\n"


def write_json(path: str | None, payload: dict) -> None:
    content = json.dumps(payload, indent=2, sort_keys=True) + "\n"
    if path:
        Path(path).write_text(content, encoding="utf-8")
    else:
        print(content, end="")


def command_plan(args: argparse.Namespace) -> int:
    plan = build_plan(args.head_ref)
    if args.write and plan["modules"]:
        apply_plan(plan)
    if args.markdown:
        Path(args.markdown).write_text(render_plan(plan), encoding="utf-8")
    write_json(args.json_output, plan)
    return 0


def command_changes(args: argparse.Namespace) -> int:
    changes = manifest_changes(args.base_ref, args.head_ref)
    if changes["modules"]:
        validate_pending_release(changes)
    write_json(args.json_output, changes)
    return 0


def command_local_tags(args: argparse.Namespace) -> int:
    changes = manifest_changes(args.base_ref, args.head_ref)
    if changes["bootstrap"]:
        raise ReleaseError("cannot create pending tags from a bootstrap manifest diff")
    create_local_tags(changes, args.target_ref)
    return 0


def command_publish(args: argparse.Namespace) -> int:
    changes = manifest_changes(args.base_ref, args.head_ref)
    if changes["bootstrap"]:
        raise ReleaseError("cannot publish tags from a bootstrap manifest diff")
    publish_tags(changes, args.target_ref, args.remote, args.push)
    if args.json_output:
        write_json(args.json_output, changes)
    return 0


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    subparsers = result.add_subparsers(dest="command", required=True)

    plan = subparsers.add_parser("plan", help="find affected modules and compute versions")
    plan.add_argument("--head-ref", default="HEAD")
    plan.add_argument("--write", action="store_true")
    plan.add_argument("--json-output")
    plan.add_argument("--markdown")
    plan.set_defaults(func=command_plan)

    changes = subparsers.add_parser("changes", help="inspect a committed manifest version diff")
    changes.add_argument("--base-ref", required=True)
    changes.add_argument("--head-ref", default="HEAD")
    changes.add_argument("--json-output")
    changes.set_defaults(func=command_changes)

    local_tags = subparsers.add_parser("local-tags", help="simulate pending tags at a local ref")
    local_tags.add_argument("--base-ref", required=True)
    local_tags.add_argument("--head-ref", default="HEAD")
    local_tags.add_argument("--target-ref", default="HEAD")
    local_tags.set_defaults(func=command_local_tags)

    publish = subparsers.add_parser("publish", help="create immutable namespaced tags")
    publish.add_argument("--base-ref", required=True)
    publish.add_argument("--head-ref", default="HEAD")
    publish.add_argument("--target-ref", default="HEAD")
    publish.add_argument("--remote", default="origin")
    publish.add_argument("--push", action="store_true")
    publish.add_argument("--json-output")
    publish.set_defaults(func=command_publish)
    return result


def main() -> int:
    try:
        args = parser().parse_args()
        return args.func(args)
    except (ReleaseError, subprocess.CalledProcessError) as exc:
        if isinstance(exc, subprocess.CalledProcessError) and exc.stderr:
            print(exc.stderr, file=sys.stderr, end="")
        print(f"module-release: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
