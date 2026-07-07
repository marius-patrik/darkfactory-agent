"""Engine interface contract — §03 I3.

Every inference engine sits behind this contract.  inferctl (inference-engine/services/inferctl)
instantiates concrete adapters; the gateway and agent runtime consume only this
interface.

4.0 core engines (two concrete adapters to build):
  - vllm       — GPU tier, OpenAI-compatible HTTP server, direct (no Dynamo yet)
  - llamacpp   — GGUF fallback / CPU+RAM hybrid

4.0 on-demand heavy tier (built last in VS4b):
  - ktransformers — 671B-class MoE, expert-offload to RAM, GPU hot-path

Post-4.0 RESERVED (seam kept visible; factory raises NotImplementedError):
  - dynamo     — NVIDIA Dynamo disagg prefill/decode, KV-aware routing,
                 multi-node TP; drops in once contracts stabilise post-4.0

Design refs: .plans/design/03-inferctl.md §I3; EXECUTION-STATE.md D-028.
"""

from __future__ import annotations

import enum
from dataclasses import dataclass, field
from typing import Protocol, runtime_checkable

__all__ = [
    # Kind constants
    "EngineKind",
    "VLLM",
    "LLAMACPP",
    "KTRANSFORMERS",
    "DYNAMO",
    # Core types
    "NodePlacement",
    "ResourceBudget",
    "CapabilityFlags",
    "EngineDescriptor",
    # Adapter contract  (§03 I3: start/health/stop/capabilities)
    "EngineAdapter",
    # Seam factory (raises NotImplementedError for RESERVED kinds)
    "engine_adapter_factory",
]


# ---------------------------------------------------------------------------
# Engine kind constants
# ---------------------------------------------------------------------------


class EngineKind(str, enum.Enum):
    """The set of known engine kinds, including reserved post-4.0 kinds.

    Using ``str`` as the mixin base lets these values round-trip through JSON
    and YAML without a custom serialiser.
    """

    vllm = "vllm"
    """GPU tier — vLLM (and/or TRT-LLM) OpenAI-compatible server.

    4.0 core.  Runs on one or more 3090s.  Direct (no Dynamo mediator) for
    4.0; Dynamo wraps it post-4.0 via the RESERVED ``dynamo`` kind.
    """

    llamacpp = "llamacpp"
    """GGUF fallback / CPU+RAM hybrid — llama.cpp server.

    4.0 core.  Broad GGUF format compatibility; CPU-only or GPU-offload via
    ``-ngl``.  Also the current host for the 1 M-context conversation model
    served from RAM (§03 I4, D-023).
    """

    ktransformers = "ktransformers"
    """On-demand heavy-reasoning RAM tier — KTransformers.

    4.0, built last (VS4b).  Serves a 671B-class MoE (e.g. DeepSeek-R1/V3
    Q4 ≈ 400 GB) by offloading experts to s002's 503 GB RAM while keeping
    the active attention path on the 3090 GPU.  Co-budgeted with other RAM
    consumers — see ``ResourceBudget.ram_co_budget_gb`` and D-028.
    """

    dynamo = "dynamo"
    """RESERVED — NVIDIA Dynamo disaggregated GPU serving (post-4.0).

    Dynamo provides KV-cache-aware routing, disaggregated prefill/decode,
    and multi-node tensor-parallel across the 2×3090 cluster.  It is gated
    on the NIC upgrade (10 GbE / RDMA) and the engine contracts stabilising
    over real 4.0 usage.

    Registering the name now keeps the seam visible.  Any attempt to
    instantiate via ``engine_adapter_factory`` raises ``NotImplementedError``
    until a concrete adapter is wired.

    What a future Dynamo adapter must implement:
      - The full ``EngineAdapter`` Protocol (see below): ``start``, ``health``,
        ``stop``, ``capabilities``.
      - ``start(profile, model, nodes)`` must call Dynamo's deployment API /
        CRDs rather than managing a local process, and return Dynamo's
        per-model routed OpenAI base URL (KV-aware routing is internal to
        Dynamo; the contract surface is unchanged).
      - ``capabilities()`` must advertise ``CapabilityFlags.multi_node_tp``
        when cross-node TP is active, and ``CapabilityFlags.kv_aware_routing``
        always.
      - ``health()`` must aggregate the health of all Dynamo workers for the
        given model, not just the Dynamo control plane.
      - ``stop()`` must delete the Dynamo deployment for the model.
    """


# Convenience aliases — use these in all inferctl + adapter code.
VLLM = EngineKind.vllm
LLAMACPP = EngineKind.llamacpp
KTRANSFORMERS = EngineKind.ktransformers
DYNAMO = EngineKind.dynamo


# ---------------------------------------------------------------------------
# NodePlacement
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class NodePlacement:
    """Where an engine instance runs.

    For 4.0 single-node bring-up the ``node_id`` is always ``"s002"`` or
    ``"s001"``; multi-node tensor-parallel (``EngineKind.dynamo`` post-4.0)
    lists both.

    Attributes:
        node_id:   Primary node hostname / k3s node name.
        extra_nodes: Additional nodes involved (multi-node TP only).
                    Empty for all 4.0 core + on-demand engines.
    """

    node_id: str
    extra_nodes: tuple[str, ...] = field(default_factory=tuple)


# ---------------------------------------------------------------------------
# ResourceBudget
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class ResourceBudget:
    """VRAM and RAM budgets for one engine instance.

    All values are in **gibibytes (GiB)** and represent *soft limits* that
    inferctl uses during placement.  Hard enforcement is via k3s resource
    requests/limits.

    RAM co-budgeting (D-028 / §03 I4):
    ``ram_co_budget_gb`` is the share of RAM *this engine may use at peak*
    when other RAM consumers are also active.  inferctl sums co-budgets across
    all running engines per node and refuses to start a new engine if the sum
    would exceed ``node_ram_gb``.  The key constraint: the KTransformers
    671B-MoE (≈ 400 GiB), the training-offload workspace, and the live brain
    worker do NOT all peak simultaneously — so individual co-budgets are
    assigned accordingly rather than summing all weights naively.

    Attributes:
        vram_gb:          GPU VRAM reservation (0 = CPU-only).
        ram_gb:           RAM reservation for this engine's model weights +
                          KV cache.
        ram_co_budget_gb: Peak RAM share for co-budget accounting.  Must be
                          >= ``ram_gb``.  Defaults to ``ram_gb`` when not
                          explicitly set (conservative: assume always at peak).
    """

    vram_gb: float = 0.0
    ram_gb: float = 0.0
    ram_co_budget_gb: float | None = None  # None → resolved to ram_gb at runtime

    def effective_co_budget(self) -> float:
        """Return the co-budget, falling back to ``ram_gb``."""
        if self.ram_co_budget_gb is None:
            return self.ram_gb
        return self.ram_co_budget_gb


# ---------------------------------------------------------------------------
# CapabilityFlags
# ---------------------------------------------------------------------------


@dataclass(frozen=True, slots=True)
class CapabilityFlags:
    """Boolean capability advertisement for one engine instance.

    inferctl reads these after ``health()`` passes to populate the gateway
    registry entry for model routing decisions.

    Attributes:
        lora_adapters:      Engine supports hot-loading LoRA adapters without
                            full model reload (required for the autolearn
                            adapter flywheel, §17).
        max_context:        Maximum context length (tokens) the engine can
                            serve for the loaded model.  0 = unknown.
        embeddings:         Engine exposes an embeddings endpoint
                            (``/v1/embeddings``).
        reranking:          Engine exposes a reranking endpoint.
        multi_node_tp:      Engine is running multi-node tensor-parallel.
                            False for all 4.0 engines (1 GbE constraint).
        kv_aware_routing:   Engine (or its front-proxy) performs KV-cache-
                            aware request routing.  False until Dynamo lands.
        streaming:          Engine supports SSE streaming completions.
        function_calling:   Engine supports the OpenAI function-calling /
                            tool-use protocol.
    """

    lora_adapters: bool = False
    max_context: int = 0
    embeddings: bool = False
    reranking: bool = False
    multi_node_tp: bool = False
    kv_aware_routing: bool = False
    streaming: bool = True
    function_calling: bool = False


# ---------------------------------------------------------------------------
# EngineDescriptor
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class EngineDescriptor:
    """Static description of one engine instance.

    One ``EngineDescriptor`` corresponds to one logical engine instance that
    inferctl manages.  Multiple descriptors may exist for the same
    ``EngineKind`` when running on different nodes.

    Attributes:
        id:           Unique instance identifier within the cluster.
                      Convention: ``"<kind>-<node_id>-<model_short>"``,
                      e.g. ``"vllm-s002-qwen2-32b"``.
        kind:         The engine kind (``EngineKind`` enum value).
        placement:    Node placement for this instance.
        models:       Model IDs this engine is currently serving or will serve
                      after ``start`` completes.  Uses the canonical
                      ``~/.rommie/models`` manifest IDs.
        budget:       VRAM + RAM budget for placement decisions.
        capabilities: Capability flags — populated after ``health()`` returns
                      ``True`` for the first time.
        profile:      Deployment profile name from §03 I3
                      (e.g. ``"dynamo-gpu"``, ``"ktransformers-ram-moe"``,
                      ``"llamacpp-gguf"``).  Informational; inferctl uses it
                      for logging and registry publication.
    """

    id: str
    kind: EngineKind
    placement: NodePlacement
    models: list[str] = field(default_factory=list)
    budget: ResourceBudget = field(default_factory=ResourceBudget)
    capabilities: CapabilityFlags = field(default_factory=CapabilityFlags)
    profile: str = ""


# ---------------------------------------------------------------------------
# EngineAdapter Protocol
# ---------------------------------------------------------------------------


@runtime_checkable
class EngineAdapter(Protocol):
    """The one interface every inference engine sits behind.

    Concrete adapters live in ``agent/agent/engines/<kind>/`` and are
    instantiated by ``engine_adapter_factory``.  inferctl calls these methods;
    nothing else should call engine processes directly.

    This surface is the **verbatim §03 I3 engine contract** (line 29 of
    ``.plans/design/03-inferctl.md``):

        start(profile, model, nodes) → endpoint
        health()
        stop()
        capabilities()

    Any extension to this surface requires a ratifying design update to §03 I3
    and a new decision entry in ``EXECUTION-STATE.md`` BEFORE the code lands.

    All methods are synchronous in this first cut (VS0 contract phase).
    Async variants will be added as a non-breaking extension once the first
    concrete adapter (vLLM or llama.cpp) is wired in VS1.

    Contract guarantees:
      - ``start()`` is idempotent when called for a model already running on
        this engine; it must return the endpoint URL without re-launching.
      - ``stop()`` is safe to call when the engine is already stopped.
      - ``health()`` never raises; it returns ``False`` on any error.
    """

    def start(
        self,
        profile: str,
        model: str,
        nodes: list[NodePlacement],
    ) -> str:
        """Bring up the engine for *model* using the given *profile* and
        *nodes*, then return the OpenAI-compatible base URL (the endpoint).

        This is the ``start(profile, model, nodes) → endpoint`` entry from
        §03 I3.

        Parameters:
            profile: Deployment profile name (e.g. ``"dynamo-gpu"``,
                     ``"llamacpp-gguf"``, ``"ktransformers-ram-moe"``).
                     Controls how the engine process / pod is launched.
            model:   Canonical model ID from the ``~/.rommie/models``
                     manifest.  inferctl guarantees weights are present on
                     the placement node(s) before calling ``start``
                     (§03 I4 weight-staging).
            nodes:   Node placement list.  A single-element list is the
                     normal case for 4.0 per-node placement.  Multi-node
                     tensor-parallel (``dynamo-tp-multinode``) passes
                     multiple entries, but that profile is RESERVED post-4.0.

        Returns:
            The OpenAI-compatible base URL at which this engine instance is
            now serving, e.g. ``"http://s002:8000/v1"``.  inferctl publishes
            this into the gateway registry as ``api_base``.

        Raises:
            ValueError:   *model* is not in the node's model manifest, or
                          *profile* is unrecognised by this adapter.
            RuntimeError: Engine process / API could not be reached or the
                          model failed to become healthy within a reasonable
                          timeout.
        """
        ...

    def health(self) -> bool:
        """Return ``True`` iff the engine is up and serving requests.

        Never raises.  inferctl polls this at the configured cadence and
        updates the gateway registry entry accordingly.  A ``False`` return
        triggers a restart attempt per the reconcile policy (§03 I2 step 3).
        """
        ...

    def stop(self) -> None:
        """Tear down the engine process / pod and release its resources.

        Safe to call when the engine is already stopped (no-op).  inferctl
        calls this under VRAM/RAM pressure, on model eviction (§03 I4 LRU),
        and during clean cluster shutdown.

        Raises:
            RuntimeError: Engine process could not be reached for a graceful
                          shutdown (implementation may choose to force-kill
                          after a timeout rather than propagate).
        """
        ...

    def capabilities(self) -> CapabilityFlags:
        """Return the current capability flags for this engine instance.

        Called after ``health()`` first returns ``True`` and after each model
        switch (capabilities may change when a different model variant is
        loaded, e.g. a model with a longer context window or LoRA support).
        """
        ...


# ---------------------------------------------------------------------------
# Seam factory — keeps RESERVED kinds visible
# ---------------------------------------------------------------------------


def engine_adapter_factory(descriptor: EngineDescriptor) -> EngineAdapter:
    """Return a concrete ``EngineAdapter`` for *descriptor*.

    This factory is the single wiring point.  Concrete adapter modules are
    imported lazily so that the contract module itself remains stdlib-only.

    RESERVED kinds raise ``NotImplementedError`` with an explicit message
    that names the post-4.0 work item — making the seam visible at runtime
    rather than silently failing.

    Raises:
        NotImplementedError: ``descriptor.kind`` is ``EngineKind.dynamo``
                             (post-4.0 RESERVED).
        ValueError:          ``descriptor.kind`` is not a known ``EngineKind``.
    """
    match descriptor.kind:
        case EngineKind.vllm:
            from agent.engines.vllm import VllmAdapter
            return VllmAdapter(descriptor)
        case EngineKind.llamacpp:
            from agent.engines.llamacpp import LlamaCppAdapter
            return LlamaCppAdapter(descriptor)
        case EngineKind.ktransformers:
            # VS4b: import the concrete KTransformers adapter
            # from agent.engines.ktransformers import KTransformersAdapter
            # return KTransformersAdapter(descriptor)
            raise NotImplementedError(
                "KTransformers adapter not yet wired — implement in VS4b "
                "(agent/agent/engines/ktransformers.py, class KTransformersAdapter)"
            )
        case EngineKind.dynamo:
            raise NotImplementedError(
                "Dynamo adapter is RESERVED for post-4.0. "
                "NVIDIA Dynamo (disaggregated prefill/decode, KV-aware routing, "
                "multi-node TP) is deferred behind this contract until the NIC "
                "upgrade (10 GbE / RDMA) and the contracts stabilise over real "
                "4.0 usage.  See .plans/design/03-inferctl.md §I3 and "
                "EXECUTION-STATE.md D-028.  "
                "To implement: create agent/agent/engines/dynamo.py, "
                "class DynamoAdapter(EngineAdapter), and uncomment the "
                "case branch in engine_adapter_factory()."
            )
        case _:
            raise ValueError(
                f"Unknown engine kind {descriptor.kind!r}.  "
                f"Valid kinds: {[k.value for k in EngineKind]}"
            )

