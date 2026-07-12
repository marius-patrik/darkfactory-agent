"""Runtime switcher state backed by live gateway registry and cluster data."""

from __future__ import annotations

import os

from agent_os.v1.common_pb import Fabric, Host, Model, Node, Provider, SwitcherAxis, SwitcherScope, SwitcherState
from agent_os.v1.switchers_pb import SwitcherOption
from connectrpc.code import Code
from connectrpc.errors import ConnectError

from llm_gateway.registry import ModelEntry, ModelRegistry


def cluster_hosts(registry: ModelRegistry) -> dict[str, bool]:
    hosts: dict[str, bool] = {}
    for raw in os.environ.get("GATEWAY_CLUSTER_HOSTS", "").split(","):
        item = raw.strip()
        if item:
            hosts[item.split("=", 1)[0].strip()] = True
    for entry in registry.list_all():
        node = entry.extra.get("node_id") or entry.extra.get("backend_node_id")
        if isinstance(node, str) and node:
            hosts[node] = hosts.get(node, False) or entry.enabled
    return hosts


def entry_fabric(entry: ModelEntry) -> Fabric:
    if entry.extra.get("node_id") or entry.extra.get("backend_node_id"):
        return Fabric.CLUSTER
    if entry.cloud:
        return Fabric.CLOUD
    return Fabric.LOCAL


class SwitcherStore:
    """Resolve global/project/session overrides without mutating other axes."""

    def __init__(self, registry: ModelRegistry) -> None:
        self.registry = registry
        first = next(iter(registry.list_enabled()), None)
        self._global = SwitcherState(
            host="gateway",
            fabric=entry_fabric(first) if first else Fabric.LOCAL,
            provider=first.provider if first else "local",
            model=first.id if first else "",
            agent=os.environ.get("GATEWAY_DEFAULT_AGENT", "rommie"),
            scope_source=SwitcherScope.GLOBAL,
        )
        self._project: SwitcherState | None = None
        self._sessions: dict[str, SwitcherState] = {}

    def state(self, session_id: str = "") -> SwitcherState:
        source = self._sessions.get(session_id) if session_id else None
        source = source or self._project or self._global
        return _copy_state(source)

    def set(self, axis: SwitcherAxis, value: str, scope: SwitcherScope, session_id: str = "") -> SwitcherState:
        if axis is SwitcherAxis.UNSPECIFIED:
            raise ConnectError(Code.INVALID_ARGUMENT, "switcher axis is required")
        if scope is SwitcherScope.UNSPECIFIED:
            raise ConnectError(Code.INVALID_ARGUMENT, "switcher scope is required")
        if scope is SwitcherScope.SESSION and not session_id:
            raise ConnectError(Code.INVALID_ARGUMENT, "session_id is required for session scope")
        valid = {option.value for option in self.options(axis, session_id) if option.available}
        if value not in valid:
            raise ConnectError(Code.INVALID_ARGUMENT, f"{value!r} is not an available {axis.name.lower()} option")

        current = self.state(session_id)
        values = _state_values(current)
        if axis is SwitcherAxis.FABRIC:
            fabric = _fabric(value)
            candidates = self._route_entries(fabric)
            if not candidates:
                raise ConnectError(Code.INVALID_ARGUMENT, f"no enabled route is available for fabric {value!r}")
            values["fabric"] = fabric
            values["provider"] = candidates[0].provider
            values["model"] = candidates[0].id
        elif axis is SwitcherAxis.PROVIDER:
            candidates = self._route_entries(current.fabric, value)
            if not candidates:
                raise ConnectError(Code.INVALID_ARGUMENT, f"no enabled model is available for provider {value!r}")
            values["provider"] = value
            values["model"] = candidates[0].id
        else:
            values[_axis_field(axis)] = value
        values["scope_source"] = scope
        updated = SwitcherState(
            host=str(values["host"]),
            fabric=values["fabric"],  # type: ignore[arg-type]
            provider=str(values["provider"]),
            model=str(values["model"]),
            agent=str(values["agent"]),
            scope_source=values["scope_source"],  # type: ignore[arg-type]
        )
        if scope is SwitcherScope.SESSION:
            self._sessions[session_id] = updated
        elif scope is SwitcherScope.PROJECT:
            self._project = updated
        else:
            self._global = updated
        return _copy_state(updated)

    def options(self, axis: SwitcherAxis, session_id: str = "") -> list[SwitcherOption]:
        state = self.state(session_id)
        if axis is SwitcherAxis.HOST:
            hosts = [SwitcherOption(value="gateway", label="gateway", available=True)]
            hosts.extend(
                SwitcherOption(value=name, label=name, available=online, unavailable_reason="node_offline" if not online else "")
                for name, online in sorted(cluster_hosts(self.registry).items())
            )
            return hosts
        if axis is SwitcherAxis.FABRIC:
            entries = self.registry.list_all()
            available = {entry_fabric(entry) for entry in entries if entry.enabled}
            if cluster_hosts(self.registry):
                available.add(Fabric.CLUSTER)
            return [
                SwitcherOption(
                    value=fabric.name.lower(),
                    label=fabric.name.lower(),
                    available=fabric in available or fabric is Fabric.LOCAL,
                    unavailable_reason="fabric_unavailable" if fabric not in available and fabric is not Fabric.LOCAL else "",
                )
                for fabric in (Fabric.LOCAL, Fabric.CLUSTER, Fabric.CLOUD)
            ]
        if axis is SwitcherAxis.PROVIDER:
            providers = sorted({entry.provider for entry in self.registry.list_enabled() if entry_fabric(entry) == state.fabric})
            return [SwitcherOption(value=value, label=value, available=True) for value in providers]
        if axis is SwitcherAxis.MODEL:
            return [
                SwitcherOption(value=entry.id, label=entry.name, available=entry.enabled, unavailable_reason="model_unavailable" if not entry.enabled else "")
                for entry in self.registry.list_all()
                if entry_fabric(entry) == state.fabric and entry.provider == state.provider
            ]
        if axis is SwitcherAxis.AGENT:
            agents = [item.strip() for item in os.environ.get("GATEWAY_AGENTS", "rommie,claude,codex,kimi,agy").split(",") if item.strip()]
            return [SwitcherOption(value=value, label=value, available=True) for value in agents]
        raise ConnectError(Code.INVALID_ARGUMENT, "switcher axis is required")

    def _route_entries(self, fabric: Fabric, provider: str | None = None) -> list[ModelEntry]:
        return [
            entry
            for entry in self.registry.list_enabled()
            if entry_fabric(entry) == fabric and (provider is None or entry.provider == provider)
        ]

    def hosts(self) -> list[Host]:
        return [Host(id="gateway", label="gateway", online=True)] + [
            Host(id=name, label=name, online=online) for name, online in sorted(cluster_hosts(self.registry).items())
        ]

    def nodes(self) -> list[Node]:
        return [Node(id=name, role="inference", online=online) for name, online in sorted(cluster_hosts(self.registry).items())]

    def models(self) -> list[Model]:
        return [
            Model(
                id=entry.id,
                provider_id=entry.provider,
                fabric=entry_fabric(entry),
                role=entry.role,
                context_length=entry.context_length,
                quant=entry.quant or "",
                enabled=entry.enabled,
                cloud=entry_fabric(entry) is Fabric.CLOUD,
            )
            for entry in self.registry.list_all()
        ]

    def providers(self) -> list[Provider]:
        items: dict[tuple[str, Fabric], bool] = {}
        for entry in self.registry.list_all():
            fabric = entry_fabric(entry)
            key = (entry.provider, fabric)
            items[key] = items.get(key, False) or entry.enabled
        return [
            Provider(id=provider, label=provider, fabric=fabric, enabled=enabled)
            for (provider, fabric), enabled in sorted(items.items(), key=lambda item: (item[0][0], item[0][1].value))
        ]


def _copy_state(state: SwitcherState) -> SwitcherState:
    return SwitcherState(
        host=state.host,
        fabric=state.fabric,
        provider=state.provider,
        model=state.model,
        agent=state.agent,
        scope_source=state.scope_source,
    )


def _state_values(state: SwitcherState) -> dict[str, object]:
    return {
        "host": state.host,
        "fabric": state.fabric,
        "provider": state.provider,
        "model": state.model,
        "agent": state.agent,
        "scope_source": state.scope_source,
    }


def _axis_field(axis: SwitcherAxis) -> str:
    return {
        SwitcherAxis.HOST: "host",
        SwitcherAxis.FABRIC: "fabric",
        SwitcherAxis.PROVIDER: "provider",
        SwitcherAxis.MODEL: "model",
        SwitcherAxis.AGENT: "agent",
    }[axis]


def _fabric(value: str) -> Fabric:
    try:
        return Fabric[value.upper()]
    except KeyError as exc:
        raise ConnectError(Code.INVALID_ARGUMENT, f"unknown fabric {value!r}") from exc
