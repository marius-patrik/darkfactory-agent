from __future__ import annotations

import subprocess

import pytest

from agent.engines._docker import DockerClient
from agent.engines.contract import (
    DYNAMO,
    VLLM,
    EngineDescriptor,
    NodePlacement,
    ResourceBudget,
    engine_adapter_factory,
)
from agent.engines.defaults import EngineLaunchConfig
from agent.engines.vllm import VllmEngine


class FakeDocker:
    def __init__(self, *, exists: bool = False, running: bool = False) -> None:
        self.exists = exists
        self.running = running
        self.calls: list[list[str]] = []

    def __call__(self, args, **kwargs):
        command = list(args)
        self.calls.append(command)
        if command[:2] == ["docker", "inspect"] and "-f" not in command:
            return self._result(0 if self.exists else 1)
        if command[:4] == ["docker", "inspect", "-f", "{{.State.Running}}"]:
            return self._result(0, "true\n" if self.running else "false\n")
        if command[:3] == ["docker", "rm", "-f"]:
            self.exists = False
            self.running = False
            return self._result(0)
        if command[:3] == ["docker", "logs", "--tail"]:
            return self._result(0, "logs")
        if command[:2] == ["docker", "run"]:
            self.exists = True
            self.running = True
            return self._result(0)
        return self._result(0)

    @staticmethod
    def _result(returncode: int, stdout: str = "", stderr: str = ""):
        return subprocess.CompletedProcess([], returncode, stdout, stderr)


def descriptor() -> EngineDescriptor:
    return EngineDescriptor(
        id="coder-32b-awq",
        kind=VLLM,
        placement=NodePlacement("s002"),
        models=["coder-32b-awq"],
        budget=ResourceBudget(vram_gb=24, ram_gb=48),
        profile="dynamo-gpu",
    )


def dynamo_descriptor() -> EngineDescriptor:
    return EngineDescriptor(
        id="dynamo-s002-coder-32b-awq",
        kind=DYNAMO,
        placement=NodePlacement("s002", ("s001",)),
        models=["coder-32b-awq"],
        budget=ResourceBudget(vram_gb=48, ram_gb=96),
        profile="dynamo-tp-multinode",
    )


def config(**overrides) -> EngineLaunchConfig:
    values = {
        "model_path": "/models/Qwen2.5-Coder-32B-Instruct-AWQ",
        "port": 8001,
        "startup_timeout_s": 0.01,
        "health_interval_s": 0,
        "max_model_len": 32768,
        "quantization": "awq",
    }
    values.update(overrides)
    return EngineLaunchConfig(**values)


def test_start_builds_vllm_docker_args_with_cdi_gpu_flag() -> None:
    fake = FakeDocker()
    engine = VllmEngine(
        descriptor(),
        docker=DockerClient(fake),
        http_get=lambda url, timeout: 200,
        sleep=lambda seconds: None,
        config=config(),
    )

    endpoint = engine.start("dynamo-gpu", "coder-32b-awq", [NodePlacement("s002")])

    run = next(call for call in fake.calls if call[:2] == ["docker", "run"])
    assert endpoint == "http://s002:8001/v1"
    assert "--device" in run
    assert "nvidia.com/gpu=all" in run
    assert "--gpus" not in run
    assert "vllm/vllm-openai:latest" in run
    assert run[run.index("--model") + 1] == "/models/Qwen2.5-Coder-32B-Instruct-AWQ"
    assert run[run.index("--max-model-len") + 1] == "32768"
    assert run[run.index("--quantization") + 1] == "awq"


def test_dynamo_adapter_factory_is_reserved_with_clear_message() -> None:
    with pytest.raises(
        NotImplementedError,
        match="Dynamo adapter is RESERVED for post-4.0",
    ):
        engine_adapter_factory(dynamo_descriptor())


def test_start_builds_vllm_docker_args_with_gpus_fallback() -> None:
    fake = FakeDocker()
    engine = VllmEngine(
        descriptor(),
        docker=DockerClient(fake),
        http_get=lambda url, timeout: 200,
        sleep=lambda seconds: None,
        config=config(gpu_flag="gpus"),
    )

    engine.start("dynamo-gpu", "coder-32b-awq", [NodePlacement("s002")])

    run = next(call for call in fake.calls if call[:2] == ["docker", "run"])
    assert "--gpus" in run
    assert run[run.index("--gpus") + 1] == "all"
    assert "--device" not in run


def test_start_is_noop_when_existing_container_is_healthy() -> None:
    fake = FakeDocker(exists=True, running=True)
    engine = VllmEngine(
        descriptor(),
        docker=DockerClient(fake),
        http_get=lambda url, timeout: 200,
        sleep=lambda seconds: None,
        config=config(),
    )

    endpoint = engine.start("dynamo-gpu", "coder-32b-awq", [NodePlacement("s002")])

    assert endpoint == "http://s002:8001/v1"
    assert not any(call[:2] == ["docker", "run"] for call in fake.calls)
    assert not any(call[:3] == ["docker", "rm", "-f"] for call in fake.calls)


def test_start_removes_dead_existing_container_before_restart() -> None:
    fake = FakeDocker(exists=True, running=False)
    engine = VllmEngine(
        descriptor(),
        docker=DockerClient(fake),
        http_get=lambda url, timeout: 200,
        sleep=lambda seconds: None,
        config=config(),
    )

    engine.start("dynamo-gpu", "coder-32b-awq", [NodePlacement("s002")])

    assert any(call[:3] == ["docker", "rm", "-f"] for call in fake.calls)
    assert any(call[:2] == ["docker", "run"] for call in fake.calls)


def test_vllm_health_requires_health_and_models_endpoints() -> None:
    seen: list[str] = []

    def http_get(url: str, timeout: float) -> int:
        seen.append(url)
        return 200 if url.endswith("/health") else 503

    engine = VllmEngine(
        descriptor(),
        docker=DockerClient(FakeDocker(exists=True, running=True)),
        http_get=http_get,
        config=config(),
    )

    assert engine.health() is False
    assert seen == ["http://s002:8001/health", "http://s002:8001/v1/models"]


def test_unload_model_stops_and_removes_engine_container() -> None:
    fake = FakeDocker(exists=True, running=True)
    engine = VllmEngine(
        descriptor(),
        docker=DockerClient(fake),
        http_get=lambda url, timeout: 200,
        config=config(),
    )

    engine.stop()

    assert any(call == ["docker", "rm", "-f", "rommie-engine-coder-32b-awq"] for call in fake.calls)
