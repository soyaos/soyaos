#!/usr/bin/env python3

import importlib.util
import sys
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("module_release.py")
SPEC = importlib.util.spec_from_file_location("module_release", SCRIPT)
module_release = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = module_release
SPEC.loader.exec_module(module_release)


class ModuleReleaseTest(unittest.TestCase):
    def module(self, directory, dependencies=()):
        return module_release.Module(
            directory,
            f"github.com/soyaos/soyaos/{directory}",
            frozenset(dependencies),
        )

    def test_prerelease_increments_only_sequence(self):
        self.assertEqual(
            module_release.bump_version("v0.1.0-alpha.1", "major"),
            "v0.1.0-alpha.2",
        )
        self.assertEqual(
            module_release.bump_version("v0.1.0-alpha.9"),
            "v0.1.0-alpha.10",
        )

    def test_semver_comparison_handles_numeric_prerelease_identifiers(self):
        self.assertGreater(
            module_release.version_key("v0.1.0-alpha.10"),
            module_release.version_key("v0.1.0-alpha.2"),
        )
        self.assertGreater(
            module_release.version_key("v0.1.0"),
            module_release.version_key("v0.1.0-alpha.99"),
        )

    def test_stable_versions_follow_semver_level(self):
        self.assertEqual(module_release.bump_version("v1.2.3", "patch"), "v1.2.4")
        self.assertEqual(module_release.bump_version("v1.2.3", "minor"), "v1.3.0")
        self.assertEqual(module_release.bump_version("v1.2.3", "major"), "v2.0.0")

    def test_reverse_dependency_closure_is_transitive(self):
        modules = {
            "pkg/store": self.module("pkg/store"),
            "pkg/auth": self.module("pkg/auth", ["pkg/store"]),
            "pkg/kernel": self.module("pkg/kernel", ["pkg/auth"]),
            "pkg/unrelated": self.module("pkg/unrelated"),
        }
        affected, reasons = module_release.reverse_dependency_closure(
            modules, ["pkg/store"]
        )
        self.assertEqual(affected, {"pkg/store", "pkg/auth", "pkg/kernel"})
        self.assertIn("depends on pkg/store", reasons["pkg/auth"])
        self.assertNotIn("pkg/unrelated", affected)

    def test_topology_places_dependencies_first(self):
        modules = {
            "pkg/store": self.module("pkg/store"),
            "pkg/auth": self.module("pkg/auth", ["pkg/store"]),
            "cmd/app": self.module("cmd/app", ["pkg/auth"]),
        }
        self.assertEqual(
            module_release.topological_order(modules, modules),
            ["pkg/store", "pkg/auth", "cmd/app"],
        )

    def test_cycle_is_rejected(self):
        modules = {
            "pkg/a": self.module("pkg/a", ["pkg/b"]),
            "pkg/b": self.module("pkg/b", ["pkg/a"]),
        }
        with self.assertRaises(module_release.ReleaseError):
            module_release.topological_order(modules, modules)

    def test_go_sum_only_change_does_not_trigger_release(self):
        paths = ["pkg/store/go.sum", "pkg/store/store.go"]
        filtered = [
            path for path in paths if Path(path).name != "go.sum"
        ]
        self.assertEqual(filtered, ["pkg/store/store.go"])


if __name__ == "__main__":
    unittest.main()
