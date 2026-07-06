"""In-process WorkerLifecycle implementation for VS2."""

from __future__ import annotations

from collections.abc import AsyncIterator
from datetime import datetime
from uuid import UUID, uuid4

from agent.conductor.lifecycle import (
    ClaimConflict,
    ClaimsHookCallable,
    NonProgressHookCallable,
    WorkerEvent,
    WorkerEventKind,
    WorkerHandle,
    WorkerLifecycle,
    WorkerStatus,
    WorkerStatusCode,
    WorkSpec,
)
from agent.loop.session import SessionConfig, run_session


class InProcessWorker:
    """Run one loop session in-process while satisfying WorkerLifecycle."""

    def __init__(self) -> None:
        self._statuses: dict[UUID, WorkerStatus] = {}
        self._events: dict[UUID, list[WorkerEvent]] = {}
        self._claims_hook: ClaimsHookCallable | None = None
        self._nonprogress_hook: NonProgressHookCallable | None = None

    async def assign(self, spec: WorkSpec) -> WorkerHandle | ClaimConflict:
        """Run the work synchronously and return its handle."""
        handle = WorkerHandle(worker_id=uuid4(), session_id=spec.parent_session_id, role=spec.role, item_id=spec.item_id)
        self._events[handle.worker_id] = [WorkerEvent(WorkerEventKind.STARTED, handle, datetime.utcnow())]
        self._statuses[handle.worker_id] = WorkerStatus(handle, WorkerStatusCode.RUNNING, heartbeat_at=datetime.utcnow())
        config = SessionConfig(
            session_id=spec.parent_session_id or str(handle.worker_id),
            agent_id=str(handle.worker_id),
            goal=str(spec.inputs.get("goal", spec.task)),
            task=spec.task,
            acceptance_type=spec.acceptance or "generic",
            declared_outputs=[str(v) for v in spec.inputs.get("declared_outputs", [])],
            model=spec.model or "qwen3-8b",
            max_turns=int(spec.budget.get("max_turns", 12)),
        )
        outcome = await run_session(config)
        code = WorkerStatusCode(outcome.status.value)
        event_kind = WorkerEventKind.DONE if outcome.status.value == "useful_result" else WorkerEventKind.FAILED
        self._statuses[handle.worker_id] = WorkerStatus(handle, code, heartbeat_at=datetime.utcnow())
        self._events[handle.worker_id].append(WorkerEvent(event_kind, handle, datetime.utcnow(), result_status=code, reason=outcome.summary))
        return handle

    async def observe(self, handle: WorkerHandle) -> tuple[WorkerStatus, AsyncIterator[WorkerEvent]]:
        """Return current status and finite event stream."""
        async def stream() -> AsyncIterator[WorkerEvent]:
            for event in self._events.get(handle.worker_id, []):
                yield event

        return self._statuses[handle.worker_id], stream()

    async def cancel(self, worker_id: UUID, reason: str) -> None:
        """Cancel a worker snapshot."""
        status = self._statuses.get(worker_id)
        if status is not None:
            self._statuses[worker_id] = WorkerStatus(status.handle, WorkerStatusCode.KILLED, heartbeat_at=datetime.utcnow())
            self._events.setdefault(worker_id, []).append(WorkerEvent(WorkerEventKind.KILLED, status.handle, datetime.utcnow(), reason=reason))

    async def suspend(self, handle: WorkerHandle, reason: str = "") -> None:
        """Mark a worker suspended."""
        self._statuses[handle.worker_id] = WorkerStatus(handle, WorkerStatusCode.SUSPENDED, heartbeat_at=datetime.utcnow())
        self._events.setdefault(handle.worker_id, []).append(WorkerEvent(WorkerEventKind.SUSPENDED, handle, datetime.utcnow(), reason=reason))

    async def resume(self, handle: WorkerHandle) -> None:
        """Mark a worker running."""
        self._statuses[handle.worker_id] = WorkerStatus(handle, WorkerStatusCode.RUNNING, heartbeat_at=datetime.utcnow())
        self._events.setdefault(handle.worker_id, []).append(WorkerEvent(WorkerEventKind.RESUMED, handle, datetime.utcnow()))

    def register_claims_hook(self, hook: ClaimsHookCallable) -> None:
        """Register claim hook."""
        self._claims_hook = hook

    def register_nonprogress_hook(self, hook: NonProgressHookCallable) -> None:
        """Register non-progress hook."""
        self._nonprogress_hook = hook


assert isinstance(InProcessWorker(), WorkerLifecycle)
