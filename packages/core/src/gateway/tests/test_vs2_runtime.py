"""Regression coverage for the real VS2 Connect, WebSocket, mTLS, and budget surfaces."""

from __future__ import annotations

import asyncio
import json

import pytest
from fastapi.testclient import TestClient
from protobuf import Oneof
from starlette.websockets import WebSocketDisconnect

from agent_os.v1.common_pb import Fabric, SwitcherAxis, SwitcherScope
from agent_os.v1.session_frames_pb import ClientFrame, ServerFrame, Status, UserInput
from llm_gateway.main import app
from llm_gateway.quota import QuotaTracker
from llm_gateway.registry import ModelRegistry
from llm_gateway.router import Router, RoutingError
from llm_gateway.sessions import SessionHub
from llm_gateway.switchers import SwitcherStore
from llm_gateway.trace import TraceLogger


@pytest.fixture
def client(monkeypatch, tmp_path):
    monkeypatch.setenv("AGENTS_HOME", str(tmp_path / ".agents"))
    monkeypatch.setenv("GATEWAY_MTLS_MODE", "off")
    with TestClient(app) as value:
        yield value


def test_generated_connect_handler_serves_registry_rpc(client):
    response = client.post(
        "/agent_os.v1.RegistryService/ListModels",
        content="{}",
        headers={"content-type": "application/json"},
    )
    assert response.status_code == 200
    assert response.headers["content-type"].startswith("application/json")
    assert {model["id"] for model in response.json()["models"]} == {
        "qwen3-8b",
        "coder-32b-awq",
        "qwen2.5-7b-q4",
        "conv-7b-1m",
        "conv-14b-1m",
    }
    health = client.post(
        "/agent_os.v1.HealthService/GetHealth",
        content="{}",
        headers={"content-type": "application/json"},
    )
    assert health.status_code == 200
    assert health.json()["components"][0]["name"] == "gateway"


def test_switcher_update_preserves_unrelated_axes(monkeypatch, tmp_path):
    registry = _registry(tmp_path)
    registry._definitions["alternate-local"] = {
        "id": "alternate-local",
        "provider": "alternate",
        "model": "alternate-local",
        "api_base": "http://127.0.0.1:8002/v1",
        "role": "general",
        "context_length": 4096,
        "enabled": True,
        "cloud": False,
    }
    registry.refresh_runtime_status(force=True)
    monkeypatch.setenv("GATEWAY_CLUSTER_HOSTS", "s001=http://s001:8001")
    store = SwitcherStore(registry)
    before = store.state()
    after = store.set(SwitcherAxis.HOST, "s001", SwitcherScope.GLOBAL)
    assert after.host == "s001"
    assert after.fabric is before.fabric
    assert after.provider == before.provider
    assert after.model == before.model
    assert after.agent == before.agent
    fabrics = {option.value: option.available for option in store.options(SwitcherAxis.FABRIC)}
    assert fabrics == {"local": True, "cluster": True, "cloud": True}

    cluster = store.set(SwitcherAxis.FABRIC, "cluster", SwitcherScope.GLOBAL)
    assert cluster.fabric is Fabric.CLUSTER
    assert cluster.provider == "local"
    assert cluster.model == "cluster-coder"
    assert {option.value for option in store.options(SwitcherAxis.MODEL)} == {"cluster-coder"}

    store.set(SwitcherAxis.FABRIC, "local", SwitcherScope.GLOBAL)
    alternate = store.set(SwitcherAxis.PROVIDER, "alternate", SwitcherScope.GLOBAL)
    assert alternate.provider == "alternate"
    assert alternate.model == "alternate-local"
    assert {option.value for option in store.options(SwitcherAxis.MODEL)} == {"alternate-local"}


def test_binary_websocket_relay_supports_multiple_clients(client):
    with client.websocket_connect("/v1/sessions/relay/ws?client_id=a") as first:
        assert ServerFrame.from_binary(first.receive_bytes()).seq == 1
        with client.websocket_connect("/v1/sessions/relay/ws?client_id=b") as second:
            assert ServerFrame.from_binary(second.receive_bytes()).seq == 1
            assert ServerFrame.from_binary(second.receive_bytes()).seq == 2
            assert ServerFrame.from_binary(first.receive_bytes()).seq == 2
            first.send_bytes(ClientFrame(frame=Oneof("user_input", UserInput(text="hello"))).to_binary())
            relayed = ServerFrame.from_binary(second.receive_bytes())
            assert relayed.seq == 3
            assert relayed.frame is not None
            assert relayed.frame.field == "status"
            assert relayed.frame.value.detail == "hello"


def test_websocket_mtls_rejects_spoofed_identity_header(monkeypatch, tmp_path):
    monkeypatch.setenv("AGENTS_HOME", str(tmp_path / ".agents"))
    monkeypatch.setenv("GATEWAY_MTLS_MODE", "require")
    monkeypatch.setenv("GATEWAY_MTLS_EDGE_TOKEN", "trusted-edge-secret")
    with TestClient(app) as client:
        assert client.get("/health", headers={"x-client-cert-verified": "SUCCESS"}).status_code == 401
        with pytest.raises(WebSocketDisconnect) as rejected:
            with client.websocket_connect(
                "/v1/sessions/secure/ws",
                headers={"x-client-cert-verified": "SUCCESS"},
            ):
                pass
        assert rejected.value.code == 4401
        with client.websocket_connect(
            "/v1/sessions/secure/ws?client_id=verified",
            headers={"x-client-cert-verified": "SUCCESS", "x-gateway-edge-token": "trusted-edge-secret"},
        ) as websocket:
            assert ServerFrame.from_binary(websocket.receive_bytes()).seq == 1


@pytest.mark.asyncio
async def test_relay_drops_broken_client_without_blocking_healthy_delivery():
    class Socket:
        def __init__(self, broken: bool = False) -> None:
            self.broken = broken
            self.payloads: list[bytes] = []

        async def send_bytes(self, payload: bytes) -> None:
            if self.broken:
                raise RuntimeError("closed")
            self.payloads.append(payload)

    hub = SessionHub("ws://gateway", "gateway")
    record = hub.create(session_id="broken-relay")
    broken = Socket(broken=True)
    healthy = Socket()
    record.clients = {"broken": broken, "healthy": healthy}  # type: ignore[dict-item]
    delivered = await hub.publish(record, ServerFrame(frame=Oneof("status", Status(state="running"))))  # type: ignore[arg-type]
    assert delivered == 1
    assert list(record.clients) == ["healthy"]
    assert len(healthy.payloads) == 1


@pytest.mark.asyncio
async def test_attach_replay_is_an_atomic_boundary_before_live_delivery():
    class BlockingSocket:
        def __init__(self) -> None:
            self.started = asyncio.Event()
            self.release = asyncio.Event()
            self.sequences: list[int] = []

        async def send_bytes(self, payload: bytes) -> None:
            frame = ServerFrame.from_binary(payload)
            if not self.sequences:
                self.started.set()
                await self.release.wait()
            self.sequences.append(frame.seq)

    hub = SessionHub("ws://gateway", "gateway")
    record = hub.create(session_id="ordered-replay")
    await hub.publish(record, ServerFrame(frame=Oneof("status", Status(state="history"))))  # type: ignore[arg-type]
    socket = BlockingSocket()
    attach = asyncio.create_task(hub.attach(record, "late-client", socket))  # type: ignore[arg-type]
    await socket.started.wait()
    live = asyncio.create_task(
        hub.publish(record, ServerFrame(frame=Oneof("status", Status(state="live"))))  # type: ignore[arg-type]
    )
    await asyncio.sleep(0)
    assert live.done() is False
    socket.release.set()
    await asyncio.gather(attach, live)
    assert socket.sequences == sorted(socket.sequences)
    assert socket.sequences[:2] == [1, 2]


def test_durable_budget_exhaustion_degrades_cloud_to_local(monkeypatch, tmp_path):
    registry = _registry(tmp_path)
    budget_path = tmp_path / "credits.json"
    budget_path.write_text(
        json.dumps(
            {
                "providers": {
                    "claude": {
                        "requests": 1,
                        "tokensIn": 8,
                        "tokensOut": 4,
                        "budget": {"maxTokens": 12},
                    }
                }
            }
        ),
        encoding="utf-8",
    )
    monkeypatch.setenv("AGENTS_CREDITS", str(budget_path))
    tracer = TraceLogger(trace_dir=tmp_path / "traces")
    router = Router(registry, tracer, quota=QuotaTracker())
    try:
        assert router.resolve_model("cloud-general", allow_cloud=True).id == "local-general"
    finally:
        tracer.close()


def test_cloud_route_requires_opt_in_and_fails_closed_on_bad_budget(monkeypatch, tmp_path):
    registry = _registry(tmp_path)
    tracer = TraceLogger(trace_dir=tmp_path / "traces")
    router = Router(registry, tracer, quota=QuotaTracker())
    try:
        assert router.resolve_model("cloud-general").id == "local-general"
        assert router.resolve_model("cloud-general", allow_cloud=True).id == "cloud-general"
        budget_path = tmp_path / "bad-credits.json"
        budget_path.write_text("not-json", encoding="utf-8")
        monkeypatch.setenv("GATEWAY_BUDGETS_PATH", str(budget_path))
        assert router.resolve_model("cloud-general", allow_cloud=True).id == "local-general"
    finally:
        tracer.close()


def test_configured_budget_authority_fails_closed_for_bad_root_or_missing_provider(monkeypatch, tmp_path):
    budget_path = tmp_path / "credits.json"
    monkeypatch.setenv("GATEWAY_BUDGETS_PATH", str(budget_path))
    quota = QuotaTracker()
    budget_path.write_text("[]", encoding="utf-8")
    assert quota.is_exhausted("claude") is True
    budget_path.write_text(json.dumps({"providers": {}}), encoding="utf-8")
    assert quota.is_exhausted("claude") is True


def test_cloud_fallback_never_selects_disabled_local_model(tmp_path):
    registry = _registry(tmp_path)
    registry._definitions["local-general"]["enabled"] = False
    registry.refresh_runtime_status(force=True)
    tracer = TraceLogger(trace_dir=tmp_path / "traces")
    router = Router(registry, tracer, quota=QuotaTracker())
    try:
        with pytest.raises(RoutingError, match="no local fallback"):
            router.resolve_model("cloud-general", allow_cloud=False)
    finally:
        tracer.close()


def _registry(tmp_path) -> ModelRegistry:
    registry_path = tmp_path / "models.yaml"
    schema_path = tmp_path / "schema.json"
    schema_path.write_text(json.dumps({"type": "object"}), encoding="utf-8")
    registry_path.write_text(
        json.dumps(
            {
                "schema_version": "gateway-registry-v1",
                "models": {
                    "local-general": {
                        "id": "local-general",
                        "provider": "local",
                        "model": "local-general",
                        "api_base": "http://127.0.0.1:8001/v1",
                        "role": "general",
                        "context_length": 4096,
                        "enabled": True,
                    },
                    "cluster-coder": {
                        "id": "cluster-coder",
                        "provider": "local",
                        "model": "cluster-coder",
                        "api_base": "http://s001:8001/v1",
                        "role": "coding",
                        "context_length": 4096,
                        "enabled": True,
                        "extra": {"node_id": "s001"},
                    },
                    "cloud-general": {
                        "id": "cloud-general",
                        "provider": "claude",
                        "model": "claude-sonnet",
                        "api_base": "https://cloud.invalid/v1",
                        "role": "general",
                        "context_length": 4096,
                        "enabled": True,
                        "cloud": True,
                    },
                },
            }
        ),
        encoding="utf-8",
    )
    return ModelRegistry(registry_path, schema_path, tmp_path / "missing-inferctl.yaml")
