#!/usr/bin/env python3
"""Enforce the repository import layering contract."""

from __future__ import annotations

import argparse
import ast
import re
import sys
from pathlib import Path


REPO = Path(__file__).resolve().parents[1]
GO_IMPORT_BLOCK = re.compile(r'import\s*\((?P<body>.*?)\)', re.DOTALL)
GO_IMPORT_LINE = re.compile(r'import\s+(?:[._A-Za-z0-9]+\s+)?(?P<path>"[^"]+")')
TS_IMPORT = re.compile(r'(?:import|export)\s+(?:[^"\']+\s+from\s+)?["\'](?P<path>[^"\']+)["\']')
FORBIDDEN_TS_SCOPES = ("@andromeda/", "@agentos/")


def rel(path: Path, root: Path) -> str:
    return path.relative_to(root).as_posix()


def fail(errors: list[str]) -> int:
    if not errors:
        return 0
    for error in errors:
        print(error, file=sys.stderr)
    return 1


def go_imports(path: Path) -> list[str]:
    text = path.read_text()
    imports: list[str] = []
    for match in GO_IMPORT_LINE.finditer(text):
        imports.append(match.group("path").strip('"'))
    for block in GO_IMPORT_BLOCK.finditer(text):
        for line in block.group("body").splitlines():
            line = line.strip()
            if not line or line.startswith("//"):
                continue
            imports.extend(re.findall(r'"([^"]+)"', line))
    return imports


def check_go(root: Path) -> list[str]:
    errors: list[str] = []
    prefix = "github.com/marius-patrik/agentos/inference-engine/"
    contracts = "github.com/marius-patrik/agentos/agentos-core/contracts-go"
    for path in root.glob("services/**/*.go"):
        parts = path.relative_to(root).parts
        if len(parts) < 3 or parts[1] in {"gateway", "db"}:
            continue
        service = parts[1]
        self_mod = prefix + "services/" + service
        for imp in go_imports(path):
            if not imp.startswith(prefix):
                continue
            allowed = (imp == contracts or imp.startswith(contracts + "/")
                       or imp == self_mod or imp.startswith(self_mod + "/"))
            if service == "contracts-go":
                if not allowed:
                    errors.append(f"{rel(path, root)}: contracts-go must not import internal package {imp}")
            elif not allowed:
                errors.append(f"{rel(path, root)}: service {service} may import only contracts-go, not {imp}")
    return errors


def py_imports(path: Path) -> list[str]:
    tree = ast.parse(path.read_text(), filename=str(path))
    imports: list[str] = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            imports.extend(alias.name for alias in node.names)
        elif isinstance(node, ast.ImportFrom) and node.module:
            imports.append(node.module)
    return imports


def check_python(root: Path) -> list[str]:
    errors: list[str] = []
    forbidden = ("services", "clients", "plugins")
    for path in root.glob("agent/**/*.py"):
        for imp in py_imports(path):
            if imp.startswith(forbidden):
                errors.append(f"{rel(path, root)}: agent may import only generated contract stubs, not {imp}")
    return errors


def check_ts(root: Path) -> list[str]:
    errors: list[str] = []
    allowed = ("@agentos/shared-ts", "@agentos/generated")
    for path in root.glob("clients/**/*.ts"):
        package = path.relative_to(root).parts[1]
        for match in TS_IMPORT.finditer(path.read_text()):
            imp = match.group("path")
            if imp.startswith("."):
                continue
            if package == "shared-ts" and imp.startswith(FORBIDDEN_TS_SCOPES) and not imp.startswith(allowed):
                errors.append(f"{rel(path, root)}: shared-ts must not import client package {imp}")
            elif package != "shared-ts" and imp.startswith(FORBIDDEN_TS_SCOPES) and not imp.startswith(allowed):
                errors.append(f"{rel(path, root)}: clients may import only shared-ts/generated stubs, not {imp}")
    return errors


def check_plugins(root: Path) -> list[str]:
    errors: list[str] = []
    for path in list(root.glob("plugins/**/*.py")) + list(root.glob("plugins/**/*.ts")) + list(root.glob("plugins/**/*.go")):
        text = path.read_text()
        for forbidden in ("services/", "agent", "@andromeda/tui", "@andromeda/web", "@agentos/tui", "@agentos/web"):
            if forbidden in text:
                errors.append(f"{rel(path, root)}: plugins may import only public contracts")
                break
    return errors


def main() -> int:
    if sys.version_info < (3, 10):
        print("check_layering.py needs Python >= 3.10 (repo sources use 3.12 syntax)", file=sys.stderr)
        return 2
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=REPO)
    args = parser.parse_args()
    root = args.root.resolve()
    errors = check_go(root) + check_python(root) + check_ts(root) + check_plugins(root)
    return fail(errors)


if __name__ == "__main__":
    raise SystemExit(main())

