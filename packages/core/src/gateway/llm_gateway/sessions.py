"""In-process protobuf WebSocket session log and multi-client relay."""

from __future__ import annotations

import asyncio
import os
import uuid
from dataclasses import dataclass, field

from agent_os.v1.common_pb import RunStatus
from agent_os.v1.session_frames_pb import AttachState, ServerFrame, SessionEvent, SessionEventKind
from agent_os.v1.sessions_pb import AttachInfo, Session
from fastapi import WebSocket
from protobuf import Oneof


@dataclass
class SessionRecord:
    id: str
    title: str
    agent: str
    owner_node: str
    live: bool = True
    history: list[ServerFrame] = field(default_factory=list)
    clients: dict[str, WebSocket] = field(default_factory=dict)
    next_seq: int = 1
    relay_lock: asyncio.Lock = field(default_factory=asyncio.Lock, repr=False)

    def message(self) -> Session:
        return Session(
            id=self.id,
            title=self.title,
            agent=self.agent,
            owner_node=self.owner_node,
            status=RunStatus.RUNNING,
            live=self.live,
            attached_clients=len(self.clients),
            worker_count=0,
        )


class SessionHub:
    def __init__(self, ws_base_url: str, owner_node: str) -> None:
        self.ws_base_url = ws_base_url.rstrip("/")
        self.owner_node = owner_node
        self._sessions: dict[str, SessionRecord] = {}

    def create(self, agent: str = "", title: str = "", *, session_id: str | None = None) -> SessionRecord:
        sid = session_id or f"gateway-{uuid.uuid4().hex[:16]}"
        if sid in self._sessions:
            raise ValueError(f"session {sid!r} already exists")
        record = SessionRecord(sid, title or sid, agent or "rommie", self.owner_node)
        self._sessions[sid] = record
        return record

    def get_or_create(self, session_id: str) -> SessionRecord:
        return self._sessions.get(session_id) or self.create(session_id=session_id)

    def get(self, session_id: str) -> SessionRecord | None:
        return self._sessions.get(session_id)

    def list(self, filter_text: str = "", live_only: bool = False) -> list[SessionRecord]:
        needle = filter_text.casefold()
        return [
            record
            for record in self._sessions.values()
            if (not needle or needle in record.id.casefold() or needle in record.title.casefold() or needle in record.agent.casefold())
            and (not live_only or record.live)
        ]

    def fork(self, session_id: str, title: str = "", at_turn_id: str = "") -> SessionRecord:
        source = self._sessions[session_id]
        forked = self.create(agent=source.agent, title=title or f"{source.title} (fork)")
        history = source.history
        if at_turn_id:
            try:
                branch_seq = int(at_turn_id)
            except ValueError as exc:
                raise ValueError("at_turn_id must be a numeric server-frame sequence") from exc
            if branch_seq < 0 or branch_seq >= source.next_seq:
                raise ValueError("at_turn_id is outside the retained session log")
            if source.history and branch_seq not in {0} and branch_seq < source.history[0].seq:
                raise ValueError("at_turn_id is older than the retained session log")
            history = [frame for frame in history if frame.seq <= branch_seq]
        forked.history = [ServerFrame.from_binary(frame.to_binary()) for frame in history]
        forked.next_seq = (forked.history[-1].seq + 1) if forked.history else 1
        return forked

    def attach_info(self, record: SessionRecord) -> AttachInfo:
        return AttachInfo(
            session_id=record.id,
            ws_url=f"{self.ws_base_url}/v1/sessions/{record.id}/ws",
            owner_node=record.owner_node,
        )

    async def attach(self, record: SessionRecord, client_id: str, websocket: WebSocket) -> None:
        async with record.relay_lock:
            for frame in record.history:
                await websocket.send_bytes(frame.to_binary())
            record.clients[client_id] = websocket
        await self.publish_attach(record, client_id, "attached")

    async def detach(self, record: SessionRecord, client_id: str) -> None:
        async with record.relay_lock:
            detached = record.clients.pop(client_id, None) is not None
        if detached:
            await self.publish_attach(record, client_id, "detached")

    async def publish_attach(self, record: SessionRecord, client_id: str, action: str) -> None:
        event = SessionEvent(
            kind=SessionEventKind.ATTACH,
            payload=Oneof("attach", AttachState(client_id=client_id, action=action, attached_clients=len(record.clients))),  # type: ignore[arg-type]
        )
        await self.publish(record, ServerFrame(frame=Oneof("session_event", event)))  # type: ignore[arg-type]

    async def publish(self, record: SessionRecord, frame: ServerFrame, *, exclude: str = "") -> int:
        async with record.relay_lock:
            return await self._publish_locked(record, frame, exclude=exclude)

    async def _publish_locked(self, record: SessionRecord, frame: ServerFrame, *, exclude: str = "") -> int:
        frame.seq = record.next_seq
        record.next_seq += 1
        record.history.append(ServerFrame.from_binary(frame.to_binary()))
        history_limit = max(1, int(os.environ.get("GATEWAY_SESSION_HISTORY_FRAMES", "1000")))
        if len(record.history) > history_limit:
            del record.history[: len(record.history) - history_limit]
        delivered = 0
        broken: list[str] = []
        payload = frame.to_binary()
        for client_id, websocket in list(record.clients.items()):
            if client_id == exclude:
                continue
            try:
                await websocket.send_bytes(payload)
                delivered += 1
            except Exception:
                broken.append(client_id)
        for client_id in broken:
            record.clients.pop(client_id, None)
        return delivered
