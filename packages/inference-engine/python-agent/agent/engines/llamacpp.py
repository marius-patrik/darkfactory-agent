"""llama.cpp Docker-managed engine adapter."""

from __future__ import annotations

import time
import urllib.error
import urllib.request
from collections.abc import Callable

from agent.engines._docker import DockerClient, container_name
from agent.engines.contract import CapabilityFlags, EngineDescriptor, LLAMACPP, NodePlacement
from agent.engines.defaults import EngineLaunchConfig, launch_config_for


HttpGet = Callable[[str, float], int]


class LlamaCppEngine:
    image = "ghcr.io/ggml-org/llama.cpp:server"

    def __init__(
        self,
        descriptor: EngineDescriptor,
        *,
        docker: DockerClient | None = None,
        http_get: HttpGet | None = None,
        sleep: Callable[[float], None] | None = None,
        config: EngineLaunchConfig | None = None,
    ) -> None:
        if descriptor.kind != LLAMACPP:
            raise ValueError(f"LlamaCppEngine requires kind {LLAMACPP.value!r}")
        self.descriptor = descriptor
        self.docker = docker or DockerClient()
        self._http_get = http_get or _http_status
        self._sleep = sleep or time.sleep
        self._config = config
        self._host = descriptor.placement.node_id
        self._container = container_name(descriptor.id)

    def start(self, profile: str, model: str, nodes: list[NodePlacement]) -> str:
        if profile != "llamacpp-gguf":
            raise ValueError(f"llama.cpp adapter does not support profile {profile!r}")
        config = self._resolve_config(model)
        self._host = config.host or (nodes[0].node_id if nodes else self.descriptor.placement.node_id)
        args = self._docker_run_args(config)
        self.docker.ensure_model(self._container, args, self.health)
        if not self._wait_healthy(config):
            logs = self.docker.logs(self._container)
            raise RuntimeError(f"llama.cpp model {model!r} did not become healthy: {logs}")
        return f"{self._base_url(config)}/v1"

    def health(self) -> bool:
        try:
            return _healthy(self._http_get, f"{self._base_url(self._current_config())}/health")
        except Exception:
            return False

    def stop(self) -> None:
        self.docker.unload_model(self._container)

    def capabilities(self) -> CapabilityFlags:
        return self._current_config().capabilities

    def _resolve_config(self, model: str) -> EngineLaunchConfig:
        if self._config is None:
            self._config = launch_config_for(self.descriptor, model)
        return self._config

    def _current_config(self) -> EngineLaunchConfig:
        if self._config is not None:
            return self._config
        model = self.descriptor.models[0] if self.descriptor.models else self.descriptor.id
        return self._resolve_config(model)

    def _base_url(self, config: EngineLaunchConfig) -> str:
        return f"http://{self._host}:{config.port}"

    def _wait_healthy(self, config: EngineLaunchConfig) -> bool:
        deadline = time.monotonic() + config.startup_timeout_s
        while time.monotonic() <= deadline:
            if self.health():
                return True
            self._sleep(config.health_interval_s)
        return False

    def _docker_run_args(self, config: EngineLaunchConfig) -> list[str]:
        args = [
            "docker",
            "run",
            "-d",
            "--name",
            self._container,
            "--restart",
            "unless-stopped",
            "-p",
            f"{config.port}:{config.port}",
            "-v",
            f"{config.host_models_dir}:{config.container_models_dir}:ro",
            self.image,
            "-m",
            config.container_model_path(),
            "-c",
            str(config.ctx or config.capabilities.max_context or 4096),
            "--host",
            "0.0.0.0",
            "--port",
            str(config.port),
        ]
        if config.cache_quantized:
            args += ["--cache-type-k", "q8_0", "--cache-type-v", "q8_0"]
        if config.threads is not None:
            args += ["--threads", str(config.threads)]
        return args


LlamaCppAdapter = LlamaCppEngine


def _http_status(url: str, timeout: float) -> int:
    with urllib.request.urlopen(url, timeout=timeout) as response:
        return int(response.status)


def _healthy(http_get: HttpGet, url: str) -> bool:
    try:
        status = http_get(url, 5.0)
    except (OSError, urllib.error.URLError):
        return False
    return 200 <= status < 300
