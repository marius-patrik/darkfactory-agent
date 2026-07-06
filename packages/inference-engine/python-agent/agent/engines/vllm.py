"""vLLM Docker-managed engine adapter."""

from __future__ import annotations

import time
import urllib.error
import urllib.request
from collections.abc import Callable

from agent.engines._docker import DockerClient, container_name
from agent.engines.contract import CapabilityFlags, EngineDescriptor, NodePlacement, VLLM
from agent.engines.defaults import EngineLaunchConfig, launch_config_for


HttpGet = Callable[[str, float], int]


class VllmEngine:
    image = "vllm/vllm-openai:latest"

    def __init__(
        self,
        descriptor: EngineDescriptor,
        *,
        docker: DockerClient | None = None,
        http_get: HttpGet | None = None,
        sleep: Callable[[float], None] | None = None,
        config: EngineLaunchConfig | None = None,
    ) -> None:
        if descriptor.kind != VLLM:
            raise ValueError(f"VllmEngine requires kind {VLLM.value!r}")
        self.descriptor = descriptor
        self.docker = docker or DockerClient()
        self._http_get = http_get or _http_status
        self._sleep = sleep or time.sleep
        self._config = config
        self._host = descriptor.placement.node_id
        self._container = container_name(descriptor.id)

    def start(self, profile: str, model: str, nodes: list[NodePlacement]) -> str:
        if profile != "dynamo-gpu":
            raise ValueError(f"vLLM adapter does not support profile {profile!r}")
        config = self._resolve_config(model)
        self._host = config.host or (nodes[0].node_id if nodes else self.descriptor.placement.node_id)
        args = self._docker_run_args(config)
        self.docker.ensure_model(self._container, args, self.health)
        if not self._wait_healthy(config):
            logs = self.docker.logs(self._container)
            raise RuntimeError(f"vLLM model {model!r} did not become healthy: {logs}")
        return self._endpoint(config)

    def health(self) -> bool:
        try:
            config = self._current_config()
            base = self._base_url(config)
            return _healthy(self._http_get, f"{base}/health") and _healthy(
                self._http_get, f"{base}/v1/models"
            )
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

    def _endpoint(self, config: EngineLaunchConfig) -> str:
        return f"{self._base_url(config)}/v1"

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
        ]
        match config.gpu_flag:
            case "cdi":
                args += ["--device", "nvidia.com/gpu=all"]
            case "gpus":
                args += ["--gpus", "all"]
            case "none":
                pass
            case other:
                raise ValueError(f"unsupported vLLM gpu flag {other!r}")
        args += [
            self.image,
            "--host",
            "0.0.0.0",
            "--port",
            str(config.port),
            "--model",
            config.container_model_path(),
            "--served-model-name",
            self.descriptor.id,
        ]
        if config.max_model_len is not None:
            args += ["--max-model-len", str(config.max_model_len)]
        if config.quantization:
            args += ["--quantization", config.quantization]
        args += config.extra_engine_args
        return args


VllmAdapter = VllmEngine


def _http_status(url: str, timeout: float) -> int:
    with urllib.request.urlopen(url, timeout=timeout) as response:
        return int(response.status)


def _healthy(http_get: HttpGet, url: str) -> bool:
    try:
        status = http_get(url, 5.0)
    except (OSError, urllib.error.URLError):
        return False
    return 200 <= status < 300
