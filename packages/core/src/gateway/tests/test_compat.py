"""Compatibility aliases for the previous ``agentos_gateway`` naming."""

from __future__ import annotations

import importlib
import sys
import warnings

import pytest


SUBMODULES = [
    "cli",
    "fallback",
    "health",
    "main",
    "oauth",
    "pg_registry",
    "quota",
    "registry",
    "router",
    "schemas",
    "switchers",
    "task_routing",
    "trace",
]


def test_agentos_gateway_package_alias():
    """Importing ``agentos_gateway`` warns and resolves to ``llm_gateway``."""
    # Ensure a clean import state.
    sys.modules.pop("agentos_gateway", None)
    sys.modules.pop("llm_gateway", None)

    with pytest.warns(DeprecationWarning, match="agentos_gateway is deprecated"):
        import agentos_gateway

    import llm_gateway

    assert agentos_gateway is llm_gateway


def test_agentos_gateway_submodule_aliases_same_object():
    """Deprecated submodule imports resolve to the identical ``llm_gateway`` modules."""
    for name in SUBMODULES:
        # Clear any prior imports to test both orders deterministically.
        sys.modules.pop(f"agentos_gateway.{name}", None)
        sys.modules.pop(f"llm_gateway.{name}", None)

        with warnings.catch_warnings():
            warnings.simplefilter("ignore", DeprecationWarning)
            deprecated = importlib.import_module(f"agentos_gateway.{name}")
            canonical = importlib.import_module(f"llm_gateway.{name}")

        assert deprecated is canonical, f"agentos_gateway.{name} is not llm_gateway.{name}"


def test_agentos_gateway_submodule_alias_after_canonical():
    """Loading the canonical module first still makes the alias resolve correctly."""
    for name in SUBMODULES:
        sys.modules.pop(f"agentos_gateway.{name}", None)
        sys.modules.pop(f"llm_gateway.{name}", None)

        canonical = importlib.import_module(f"llm_gateway.{name}")
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", DeprecationWarning)
            deprecated = importlib.import_module(f"agentos_gateway.{name}")

        assert deprecated is canonical, f"agentos_gateway.{name} alias failed after canonical import"
