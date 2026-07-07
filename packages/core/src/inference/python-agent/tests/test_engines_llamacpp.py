from __future__ import annotations

import subprocess

from agent.engines._docker import DockerClient
from agent.engines.contract import EngineDescriptor, LLAMACPP, NodePlacement, ResourceBudget
from agent.engines.defaults import EngineLaunchConfig
from agent.engines.llamacpp import LlamaCppEngine


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
        id="conv-14b-1m",
        kind=LLAMACPP,
        placement=NodePlacement("s002"),
        models=["conv-14b-1m"],
        budget=ResourceBudget(ram_gb=160, ram_co_budget_gb=160),
        profile="llamacpp-gguf",
    )


def config(**overrides) -> EngineLaunchConfig:
    values = {
        "model_path": "/models/conv-14b-1m-q8.gguf",
        "port": 8082,
        "startup_timeout_s": 0.01,
        "health_interval_s": 0,
        "ctx": 1_000_000,
        "cache_quantized": True,
        "threads": 44,
    }
    values.update(overrides)
    return EngineLaunchConfig(**values)


def test_start_builds_llamacpp_docker_args() -> None:
    fake = FakeDocker()
    engine = LlamaCppEngine(
        descriptor(),
        docker=DockerClient(fake),
        http_get=lambda url, timeout: 200,
        sleep=lambda seconds: None,
        config=config(),
    )

    endpoint = engine.start("llamacpp-gguf", "conv-14b-1m", [NodePlacement("s002")])

    run = next(call for call in fake.calls if call[:2] == ["docker", "run"])
    assert endpoint == "http://s002:8082/v1"
    assert "ghcr.io/ggml-org/llama.cpp:server" in run
    assert run[run.index("-m") + 1] == "/models/conv-14b-1m-q8.gguf"
    assert run[run.index("-c") + 1] == "1000000"
    assert run[run.index("--port") + 1] == "8082"
    assert run[run.index("--threads") + 1] == "44"
    assert run[run.index("--cache-type-k") + 1] == "q8_0"
    assert run[run.index("--cache-type-v") + 1] == "q8_0"


def test_start_polls_llamacpp_health_until_ready() -> None:
    fake = FakeDocker()
    statuses = iter([503, 200])
    sleeps: list[float] = []
    engine = LlamaCppEngine(
        descriptor(),
        docker=DockerClient(fake),
        http_get=lambda url, timeout: next(statuses),
        sleep=sleeps.append,
        config=config(startup_timeout_s=1, health_interval_s=0.1),
    )

    engine.start("llamacpp-gguf", "conv-14b-1m", [NodePlacement("s002")])

    assert sleeps == [0.1]
    assert any(call[:2] == ["docker", "run"] for call in fake.calls)


def test_unload_model_is_noop_when_container_does_not_exist() -> None:
    fake = FakeDocker(exists=False, running=False)
    engine = LlamaCppEngine(
        descriptor(),
        docker=DockerClient(fake),
        http_get=lambda url, timeout: 200,
        config=config(),
    )

    engine.stop()

    assert not any(call[:3] == ["docker", "rm", "-f"] for call in fake.calls)
