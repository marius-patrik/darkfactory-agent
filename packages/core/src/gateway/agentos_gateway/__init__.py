"""Deprecated compatibility alias for the ``llm_gateway`` package.

New code should import from ``llm_gateway``. Existing consumers that import
``agentos_gateway`` (or any of its submodules) will continue to work but will
receive a ``DeprecationWarning``.
"""

from __future__ import annotations

import importlib.machinery
import sys
import warnings

import llm_gateway  # noqa: F401

warnings.warn(
    "agentos_gateway is deprecated; import from llm_gateway instead",
    DeprecationWarning,
    stacklevel=2,
)


class _AliasFinder:
    """Meta-path finder that maps ``agentos_gateway.*`` to ``llm_gateway.*``."""

    _PREFIX = "agentos_gateway."

    def find_spec(self, fullname: str, path=None, target=None):
        if not fullname.startswith(self._PREFIX):
            return None
        canonical = "llm_gateway" + fullname[len("agentos_gateway") :]
        # Ensure the canonical module is loaded; importing it populates sys.modules.
        if canonical not in sys.modules:
            __import__(canonical)
        module = sys.modules[canonical]
        loader = _AliasLoader(module)
        spec = importlib.machinery.ModuleSpec(fullname, loader, origin=module.__file__)
        spec.has_location = True
        return spec


class _AliasLoader:
    """Loader that returns the already-loaded canonical module unchanged."""

    def __init__(self, module):
        self._module = module

    def create_module(self, spec):
        return self._module

    def exec_module(self, module):
        pass


# Install the alias finder before the default path finder so that any legacy
# ``agentos_gateway.X`` import resolves to the identical ``llm_gateway.X`` object.
sys.meta_path.insert(0, _AliasFinder())

# Replace this top-level module with the real ``llm_gateway`` module so that
# ``import agentos_gateway`` is literally ``import llm_gateway``.
sys.modules[__name__] = sys.modules["llm_gateway"]
