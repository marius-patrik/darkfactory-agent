"""Default VS1 engine descriptors and launch settings."""

from __future__ import annotations

from dataclasses import dataclass, field

from agent.engines.contract import (
    CapabilityFlags,
    EngineDescriptor,
    LLAMACPP,
    NodePlacement,
    ResourceBudget,
    VLLM,
)


@dataclass(frozen=True, slots=True)
class EngineLaunchConfig:
    model_path: str
    port: int
    host_models_dir: str = "/home/patrik/models"
    container_models_dir: str = "/models"
    host: str | None = None
    startup_timeout_s: float = 600.0
    health_interval_s: float = 2.0
    max_model_len: int | None = None
    quantization: str | None = None
    gpu_flag: str = "cdi"
    ctx: int | None = None
    cache_quantized: bool = False
    threads: int | None = None
    extra_engine_args: list[str] = field(default_factory=list)
    capabilities: CapabilityFlags = field(default_factory=CapabilityFlags)

    def container_model_path(self) -> str:
        if self.model_path.startswith(self.container_models_dir + "/"):
            return self.model_path
        if self.model_path.startswith("/models/"):
            return self.model_path
        return f"{self.container_models_dir}/{self.model_path.lstrip('/')}"


DEFAULT_ENGINE_DESCRIPTORS: dict[str, EngineDescriptor] = {
    "qwen3-8b": EngineDescriptor(
        id="qwen3-8b",
        kind=VLLM,
        placement=NodePlacement("s002"),
        models=["qwen3-8b"],
        budget=ResourceBudget(vram_gb=16, ram_gb=24),
        capabilities=CapabilityFlags(max_context=32768, embeddings=True),
        profile="dynamo-gpu",
    ),
    "coder-32b-awq": EngineDescriptor(
        id="coder-32b-awq",
        kind=VLLM,
        placement=NodePlacement("s002"),
        models=["coder-32b-awq"],
        budget=ResourceBudget(vram_gb=24, ram_gb=48),
        capabilities=CapabilityFlags(max_context=32768),
        profile="dynamo-gpu",
    ),
    "qwen2.5-7b-q4": EngineDescriptor(
        id="qwen2.5-7b-q4",
        kind=LLAMACPP,
        placement=NodePlacement("s001"),
        models=["qwen2.5-7b-q4"],
        budget=ResourceBudget(vram_gb=0, ram_gb=8),
        capabilities=CapabilityFlags(max_context=32768),
        profile="llamacpp-gguf",
    ),
    "conv-7b-1m": EngineDescriptor(
        id="conv-7b-1m",
        kind=LLAMACPP,
        placement=NodePlacement("s002"),
        models=["conv-7b-1m"],
        budget=ResourceBudget(vram_gb=0, ram_gb=96, ram_co_budget_gb=96),
        capabilities=CapabilityFlags(max_context=1_000_000),
        profile="llamacpp-gguf",
    ),
    "conv-14b-1m": EngineDescriptor(
        id="conv-14b-1m",
        kind=LLAMACPP,
        placement=NodePlacement("s002"),
        models=["conv-14b-1m"],
        budget=ResourceBudget(vram_gb=0, ram_gb=160, ram_co_budget_gb=160),
        capabilities=CapabilityFlags(max_context=1_000_000),
        profile="llamacpp-gguf",
    ),
}


DEFAULT_ENGINE_CONFIGS: dict[str, EngineLaunchConfig] = {
    "qwen3-8b": EngineLaunchConfig(
        model_path="/models/Qwen3-8B",
        port=8001,
        max_model_len=32768,
        extra_engine_args=["--enable-auto-tool-choice", "--tool-call-parser", "hermes"],
        capabilities=CapabilityFlags(max_context=32768, embeddings=True),
    ),
    "coder-32b-awq": EngineLaunchConfig(
        # 19.3G AWQ weights on the 24G 3090: KV fits only at 8k ctx with
        # raised utilization + eager mode (swap-proven live 2026-06-12).
        model_path="/models/hf-cache/hub/models--Qwen--Qwen2.5-Coder-32B-Instruct-AWQ/snapshots/1ed0a6145da0ce550c628e8e8b678f51e695995d",
        port=8002,
        max_model_len=8192,
        quantization="awq",
        extra_engine_args=[
            "--gpu-memory-utilization",
            "0.95",
            "--enforce-eager",
            "--enable-auto-tool-choice",
            "--tool-call-parser",
            "hermes",
        ],
        capabilities=CapabilityFlags(max_context=8192),
    ),
    "qwen2.5-7b-q4": EngineLaunchConfig(
        model_path="/models/Qwen2.5-7B-Instruct-Q4_K_M.gguf",
        port=8003,
        ctx=32768,
        threads=16,
        capabilities=CapabilityFlags(max_context=32768),
    ),
    "conv-7b-1m": EngineLaunchConfig(
        model_path="/models/Qwen2.5-7B-Instruct-1M-Q8_0.gguf",
        port=8004,
        ctx=1_000_000,
        cache_quantized=True,
        threads=44,
        capabilities=CapabilityFlags(max_context=1_000_000),
    ),
    "conv-14b-1m": EngineLaunchConfig(
        model_path="/models/Qwen2.5-14B-Instruct-1M-Q8_0.gguf",
        port=8005,
        ctx=1_000_000,
        cache_quantized=True,
        threads=44,
        capabilities=CapabilityFlags(max_context=1_000_000),
    ),
}


def launch_config_for(descriptor: EngineDescriptor, model: str) -> EngineLaunchConfig:
    """Resolve launch settings for a descriptor/model pair."""
    for key in (descriptor.id, model, *(descriptor.models or [])):
        config = DEFAULT_ENGINE_CONFIGS.get(key)
        if config is not None:
            return config
    raise ValueError(
        f"No launch config for descriptor {descriptor.id!r} and model {model!r}"
    )
