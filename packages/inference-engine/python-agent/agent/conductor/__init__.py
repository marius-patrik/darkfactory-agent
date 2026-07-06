"""
Conductor package — worker-lifecycle management component.

The conductor is the deterministic, decide-free mechanism that assigns, tracks,
cancels, suspends, and resumes worker runtime units on behalf of the thought and
orchestration roles.  It enforces write-scope claims, mediates blackboard I/O,
and signals non-progress — it never makes model calls or decides *what* to do.

Design authority: .plans/design/drafts/D5-orchestration-algo.md (§B),
D1-decomposition.md (§D1.2), .plans/design/15-orchestration.md (OR1/OR2),
and .plans/EXECUTION-STATE.md entry D-029.

Extraction seam (D1.2 / D-029):
    This in-process Python component sits behind the WorkerLifecycle Protocol
    (lifecycle.py).  When cluster-scale fan-out justifies it, the implementation
    moves to services/conductor (Go) while the Protocol surface stays identical.
    The thought/orchestration roles call only the Protocol — they never reach
    into the implementation.

Public surface:
    WorkSpec               — fully-describes a unit of work to be assigned
    WorkerHandle           — opaque reference returned by assign()
    WorkerStatus           — OR2-aligned status vocabulary snapshot
    WorkerEvent            — lifecycle event emitted by an active worker
    ProgressSignal         — non-progress watchdog inputs (D5 §D-I)
    ClaimScope             — write-scope typed union (D5 §C1)
    WorkerLifecycle        — the Protocol every conductor implementation must satisfy
    ClaimsHookCallable     — type alias for the claims-conflict hook callable
    NonProgressHookCallable — type alias for the non-progress-watchdog hook callable
"""

from agent.conductor.lifecycle import (
    ClaimConflict,
    ClaimScope,
    ClaimsHookCallable,
    LogicalScope,
    NonProgressHookCallable,
    PathGlobScope,
    ProgressSignal,
    TaskScope,
    WorkSpec,
    WorkerEvent,
    WorkerEventKind,
    WorkerHandle,
    WorkerLifecycle,
    WorkerSpec,
    WorkerStatus,
    WorkerStatusCode,
)

__all__ = [
    "ClaimConflict",
    "ClaimScope",
    "ClaimsHookCallable",
    "LogicalScope",
    "NonProgressHookCallable",
    "PathGlobScope",
    "ProgressSignal",
    "TaskScope",
    "WorkSpec",
    "WorkerEvent",
    "WorkerEventKind",
    "WorkerHandle",
    "WorkerLifecycle",
    "WorkerSpec",
    "WorkerStatus",
    "WorkerStatusCode",
]
