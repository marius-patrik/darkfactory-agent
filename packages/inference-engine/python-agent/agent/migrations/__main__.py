"""CLI entry point for the CAP-02 migrator."""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

from agent.migrations.cap02 import migrate, format_plan


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="python -m agent.migrations",
        description="CAP-02 migrator: current loose ~/.rommie -> final §19 schema.",
    )
    parser.add_argument(
        "--root",
        default="~/.rommie",
        help="Root path to migrate (default: ~/.rommie).",
    )
    parser.add_argument(
        "--apply",
        action="store_true",
        help="Execute the migration. Without this flag, a dry-run plan is printed.",
    )
    args = parser.parse_args(argv)

    root = Path(args.root)
    plan, manifest_path = migrate(root, apply=args.apply)
    print(format_plan(plan, applied=args.apply))

    if args.apply and manifest_path:
        print(f"\nAudit manifest: {manifest_path}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
