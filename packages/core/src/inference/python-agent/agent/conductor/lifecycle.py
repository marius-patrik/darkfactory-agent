"""
Worker-lifecycle contract — the fixed interface the conductor implements.

Design authority
----------------
- .plans/design/drafts/D5-orchestration-algo.md   — primary (§A, §B, §C, §D)
- .plans/design/drafts/D1-decomposition.md        — conductor boundary (§D1.2)
- .plans/design/15-orchestration.md               — OR1 claims, OR2 status vocab
- .plans/EXECUTION-STATE.md D-029 entry           — in-process-first, per-NODE
  instancing, claim-on-first-write, pause-aware TTL

Shape derivation notes
----------------------
* Every name and field is derived directly from the design documents above.
  Nothing is invented beyond them.  Where a document offers an ambiguous
  choice the simplest reading was taken and is noted inline with "SIMPLEST".
* The WorkerLifecycle Protocol uses Python 3.12 typing.Protocol so that both
  the in-process stub (VS0/S2) and the eventual promoted Go-backed implementation
  satisfy the same static type check.
* stdlib-only; no third-party imports.

Extraction seam (D1.2)
----------------------
This module is the one thing that must not change when conductor moves from
agent/agent/conductor/ (Python, in-process) to services/conductor (Go, NATS
RPC).  The Protocol methods are the fixed API; transport is an implementation
detail.  Callers (thought, orchestration roles) import WorkerLifecycle and
call it — they never import an implementation class.
"""

from __future__ import annotations

import enum
from collections.abc import AsyncIterator
from dataclasses import dataclass, field
from datetime import datetime
from typing import Protocol, runtime_checkable
from uuid import UUID


# ---------------------------------------------------------------------------
# OR2 — STATUS VOCABULARY
# "command-success != useful-result" (15-orchestration.md §OR2, LOCKED)
# ---------------------------------------------------------------------------

class WorkerStatusCode(enum.Enum):
    """
    Controlled vocabulary for worker / run outcomes.

    Source: 15-orchestration.md §OR2 (LOCKED).  A tool/command exiting 0 does
    NOT auto-mark a run useful_result; a validated artifact is required.

    Additional lifecycle states (running / suspended / killed) are kept in the
    same enum so a single field describes the full observable life of a worker.
    These are not OR2 terminal states but are needed for observe() snapshots.
    SIMPLEST: fold lifecycle states into one enum rather than two separate types;
    the terminal OR2 states are clearly documented below.
    """

    # --- OR2 terminal states (LOCKED, 15-orchestration.md §OR2) ---

    USEFUL_RESULT = "useful_result"
    """Worker produced a validated artifact that passed the NFG gate."""

    NO_ARTIFACT = "no_artifact"
    """Worker exited cleanly but produced no artifact (exit-0 != green)."""

    MISSING_EVIDENCE = "missing_evidence"
    """Artifact present but acceptance check or source-separation guard failed."""

    UNRESOLVED = "unresolved"
    """Work item remains open; no conclusive outcome yet (e.g. awaiting deps)."""

    BLOCKED = "blocked"
    """Worker cannot proceed; waiting on an external condition or claim release."""

    FAILED = "failed"
    """Worker crashed, exceeded ceiling, or was killed after exhausting recovery."""

    RELEASED = "released"
    """Claim released voluntarily before producing a terminal result (cooperative stop)."""

    EXPIRED = "expired"
    """Claim TTL elapsed with no renewal; worker presumed dead."""

    # --- Lifecycle states (not OR2 terminal — used in WorkerStatus snapshots) ---

    PENDING = "pending"
    """Spec accepted by conductor; claim acquired; spawn in progress."""

    RUNNING = "running"
    """Worker is active and emitting heartbeats."""

    SUSPENDED = "suspended"
    """Worker is pause-suspended (pause-aware TTL active, D-029); not expired."""

    KILLED = "killed"
    """Conductor issued a hard kill (B4 / non-progress watchdog); claim released."""


# ---------------------------------------------------------------------------
# CLAIM SCOPE — write-scope typed union (D5 §C1)
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class PathGlobScope:
    """
    Filesystem write-scope: a set of glob patterns within a repository.

    D5 §C1: "path_glob: { repo, globs: ['src/agent/**', '!src/agent/vendor/**'] }"
    Overlap test: glob languages intersect (normalized prefix + extglob intersection).
    """

    repo: str
    """Repository root identifier (e.g. 'Andromeda')."""

    globs: tuple[str, ...]
    """Glob patterns; '!' prefix = exclusion."""


@dataclass(frozen=True)
class LogicalScope:
    """
    Abstract resource write-scope (D5 §C1 logical: clause).

    Examples: 'session:<id>.plan', 'registry:capability:<name>',
    'cascade:<agent>:<session>:context.md', 'domain:<id>:state',
    'memory:<agent>:<topic>'.

    Overlap test: exact resource-id equality with parent/child rule
    ('cascade:a:s:*' parent conflicts with any child under it).

    D-029 / D5 §C1 note: LogicalScope is in the full typed union.  The simplest
    S2 build may exercise only TaskScope and PathGlobScope, but the type is
    defined here so callers never need to change call sites when logical claims
    are activated.
    """

    resource: str
    """Fully-qualified resource identifier string."""


@dataclass(frozen=True)
class TaskScope:
    """
    Coarsest scope: a whole task / run (D5 §C1 task: clause).

    Overlap test: task_id equality.
    """

    task_id: str
    """Task or run identifier."""


# SIMPLEST: union expressed as a type alias over the three frozen dataclasses.
# The conductor's overlap-test dispatches on the concrete type.
ClaimScope = PathGlobScope | LogicalScope | TaskScope


# ---------------------------------------------------------------------------
# WORKER SPEC — fully describes a unit of work to assign
# Derived from D5 §A1 WorkSpec + §B2 Assign, D1-decomposition.md §D1.2
# ---------------------------------------------------------------------------

@dataclass
class WorkSpec:
    """
    All information the conductor needs to spawn a worker and acquire its claim.

    Derived from D5 §A1:
        spec = WorkSpec(role=role, task=wi.task, acceptance=wi.acceptance,
                        inputs=wi.inputs, write_scope=scope, model=role.model,
                        budget=wi.budget, parent=session, item_id=wi.id)

    Field notes
    -----------
    role        : The role name (e.g. 'execution', 'review', 'reasoning').  The
                  conductor uses this to select the runtime lane and tool allowlist;
                  it does NOT interpret the role semantics (that is thought's job).
    model       : Model identifier string — passed through to the worker runtime.
                  The conductor never selects a model.
    tools       : Allowlist of tool names this worker may invoke.  The conductor
                  enforces the allowlist at the blackboard boundary (D5 §E2).
                  SIMPLEST: a tuple of strings; the runtime resolves them.
    write_scope : The ClaimScope the conductor must acquire before spawning.
                  claim-on-first-write (D-029): if None, the claim is registered
                  on the worker's first blackboard_write call rather than at spawn.
    budget      : Optional resource envelope.  Keys: 'tokens', 'wallclock_seconds',
                  'model_pref'.  The conductor records this in the dispatch ledger;
                  enforcement is the runtime's responsibility.
    parent_session_id : The owning session.  Per-NODE instancing (D-029): the
                  conductor instance that owns this session must handle the assign.
    """

    role: str
    task: str
    acceptance: str
    inputs: dict[str, object] = field(default_factory=dict)
    write_scope: ClaimScope | None = None
    model: str = ""
    tools: tuple[str, ...] = field(default_factory=tuple)
    budget: dict[str, object] = field(default_factory=dict)
    parent_session_id: str = ""
    item_id: str = ""


# ---------------------------------------------------------------------------
# WORKER HANDLE — opaque reference returned by assign
# Derived from D5 §B1 (worker_id) and §B2 (unit.ID)
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class WorkerHandle:
    """
    Opaque reference to a spawned worker, returned by WorkerLifecycle.assign().

    The handle is stable for the worker's lifetime; the conductor may choose to
    store additional implementation fields, but callers must treat this as opaque
    (i.e. they must not inspect fields other than worker_id).

    SIMPLEST: a UUID worker_id is the only guaranteed stable field.  The conductor
    implementation may subclass / extend this internally.
    """

    worker_id: UUID
    """Stable identifier for this worker, unique within the session."""

    session_id: str
    """The parent session this worker belongs to (D-029 per-NODE instancing)."""

    role: str
    """Role name — kept on the handle for TUI / logging convenience."""

    item_id: str
    """Work-item id from WorkSpec.item_id."""


# ---------------------------------------------------------------------------
# WORKER STATUS — snapshot returned by observe()
# ---------------------------------------------------------------------------

@dataclass
class WorkerStatus:
    """
    Point-in-time snapshot of a worker's observable state.

    Returned by WorkerLifecycle.observe() alongside the event stream.
    The status_code aligns to the OR2 vocabulary (WorkerStatusCode).

    heartbeat_at     : Timestamp of last heartbeat; None if worker has not yet
                       emitted one.  Used by thought to detect missed heartbeats
                       (D5 §B3: stalled = (now - heartbeat_at) > 3 × interval).
    progress_signal  : Most recent ProgressSignal from the worker's heartbeat.
                       None before first heartbeat or after worker terminates.
    stale_risk       : True when a blackboard_read overlaps an active write claim
                       held by another worker (D5 §C3, OR1 advisory reads).
    """

    handle: WorkerHandle
    status_code: WorkerStatusCode
    heartbeat_at: datetime | None = None
    progress_signal: ProgressSignal | None = None
    stale_risk: bool = False


# ---------------------------------------------------------------------------
# WORKER EVENT — lifecycle events emitted by an active worker
# Derived from D5 §B2 ("started / heartbeat / done / failed") and
# D5 §B1 ("worker.done / .failed / .nonprogress / .heartbeat")
# ---------------------------------------------------------------------------

class WorkerEventKind(enum.Enum):
    STARTED = "started"
    HEARTBEAT = "heartbeat"
    DONE = "done"
    FAILED = "failed"
    NON_PROGRESS = "nonprogress"
    SUSPENDED = "suspended"
    RESUMED = "resumed"
    KILLED = "killed"


@dataclass
class WorkerEvent:
    """
    A lifecycle event emitted by a worker and surfaced by observe().

    The event stream returned by observe() is the async-iterator companion to
    the WorkerStatus snapshot: the snapshot gives the current state; the stream
    gives the delta sequence.

    result_status : Populated on DONE / FAILED events; carries the OR2 terminal
                    WorkerStatusCode.
    artifact      : Populated on DONE when the worker produced an artifact.
                    Content-addressed; callers pass this to the NFG gate.
    reason        : Populated on FAILED / KILLED / NON_PROGRESS; human-readable.
    signal        : Populated on HEARTBEAT; carries the latest ProgressSignal.
    """

    kind: WorkerEventKind
    handle: WorkerHandle
    timestamp: datetime

    # optional per-event payloads
    result_status: WorkerStatusCode | None = None
    artifact: object | None = None
    reason: str | None = None
    signal: ProgressSignal | None = None


# ---------------------------------------------------------------------------
# PROGRESS SIGNAL — non-progress watchdog input (D5 §D-I)
# ---------------------------------------------------------------------------

@dataclass
class ProgressSignal:
    """
    Carried in worker heartbeats; used by the non-progress watchdog (D5 §D).

    A turn made progress iff:
        (artifact_count increased) OR
        (blackboard_hash changed AND state_digest changed)

    An identical state_digest across consecutive turns = a no-progress turn.
    The conductor counts consecutive_no_progress_turns and escalates at K
    (D5 §D-II; proposed default K=3, OPEN per the draft).

    claim_renew : True if this heartbeat should renew the worker's write claim
                  TTL (D5 §C2: renewal piggybacked on the heartbeat).
    """

    turn_count: int
    blackboard_hash: str
    artifact_count: int
    state_digest: str
    claim_renew: bool = True


# ---------------------------------------------------------------------------
# HOOKS — callbacks registered with the conductor
# Derived from D5 §C2 (claim-on-first-write) and §D (non-progress)
# ---------------------------------------------------------------------------

@dataclass
class ClaimConflict:
    """
    Returned (or surfaced via ClaimsHookCallable) when a write-scope conflict is detected.

    D5 §B2 / §C3: conductor.Assign returns CLAIM_CONFLICT{holder, scope} rather
    than blocking.  thought then serializes / re-scopes / merges (D5 §C3).
    """

    holder_worker_id: UUID
    """The worker currently holding the conflicting claim."""

    conflicting_scope: ClaimScope
    """The portion of the requested scope that overlaps the holder's claim."""

    item_id: str
    """The work item the holder was spawned for."""


# SIMPLEST: hooks are plain callables (no ABC / registration ceremony).
# The conductor calls them synchronously from within assign / watchdog paths.
# Implementors may wrap them in async if needed — that is an implementation
# detail not visible to callers.
#
# Hook type aliases are defined after the Protocol body (below) as
# ClaimsHookCallable and NonProgressHookCallable — those are the canonical
# names callers should import and use to annotate hook variables.
# See the bottom of this module and __init__.py for re-exports.


# ---------------------------------------------------------------------------
# WorkerLifecycle PROTOCOL — the fixed conductor contract
# Derived from D5 §B1 (contract surface table) and D1.2 (WorkerLifecycle contract)
#
# This Protocol is what thought / orchestration roles import and call.
# It is the extraction seam: moving conductor from in-process to services/conductor
# (Go) changes only the implementing class, never this Protocol.
# ---------------------------------------------------------------------------

@runtime_checkable
class WorkerLifecycle(Protocol):
    """
    The conductor's fixed public contract.

    Implements D5 §B1 (the contract surface table) as a Python Protocol so that:
    - The in-process stub (VS0/S2) and the eventual Go-backed NATS-RPC proxy
      both satisfy the same static type check.
    - thought / orchestration roles can be tested against a mock that satisfies
      the Protocol without importing any implementation.

    Per-NODE instancing (D-029): one conductor instance is bound to one agent
    session and lives on the node that owns that session.  The Protocol does not
    model the binding — callers obtain the conductor from the session context.

    Method catalogue (D5 §B1)
    --------------------------
    assign(spec)    → WorkerHandle | ClaimConflict  [D5 §B1/§B2]
    status(worker_id) → WorkerStatus snapshot       [D5 §B1; via observe()]
    observe(handle) → status snapshot + event stream
    cancel(worker_id, reason)  [D5 §B1/§B4]
    suspend         → pause-aware TTL (D-029); cooperative stop + claim freeze
    resume          → re-activate a suspended worker; claim TTL reset
    register_claims_hook    → claim-on-first-write callback (D-029)
    register_nonprogress_hook → non-progress escalation callback (D5 §D)

    Note on D1.2 name: D1.2 §D1.5 lists 'spawn(role,tools,budget,claims)'
    informally; the binding contract table is D5 §B1 which uses 'assign'.
    """

    # ------------------------------------------------------------------
    # assign — D5 §B1/§B2 Assign; D1.2 "spawn(role,tools,budget,claims)" (informal name)
    # ------------------------------------------------------------------

    async def assign(
        self,
        spec: WorkSpec,
    ) -> WorkerHandle | ClaimConflict:
        """
        Acquire the write-scope claim (unless claim-on-first-write, D-029) and
        spawn a worker runtime unit for the given spec.

        Returns WorkerHandle on success.
        Returns ClaimConflict when the requested write_scope overlaps an active
        claim; the caller (thought) then serializes / re-scopes / merges per
        D5 §C3.

        The conductor:
        1. Acquires the write-scope claim (or defers to first blackboard_write
           if spec.write_scope is None — claim-on-first-write per D-029).
        2. Selects the runtime lane (in-process sub-session at VS0/S2; k3s Jobs
           at S3+; per D5 §B2 lane selection).
        3. Inserts a dispatch-ledger row (Postgres: worker_id, item, role,
           model, host, claim_id, started_at, state=pending, heartbeat_at).
        4. Arms the non-progress watchdog and heartbeat monitor (D5 §B3).
        """
        ...

    # ------------------------------------------------------------------
    # observe — D5 §B1 status(worker_id) + event stream
    # ------------------------------------------------------------------

    async def observe(
        self,
        handle: WorkerHandle,
    ) -> tuple[WorkerStatus, AsyncIterator[WorkerEvent]]:
        """
        Return the current WorkerStatus snapshot and an async event stream.

        The snapshot gives the present state (suitable for a one-shot poll).
        The async iterator emits WorkerEvent objects as the worker progresses:
        STARTED → HEARTBEAT* → (SUSPENDED? RESUMED?)* → DONE | FAILED | KILLED.

        The stream ends when the worker reaches a terminal state.

        SIMPLEST: one coroutine returns both snapshot and stream so callers can
        immediately interrogate state without a race between two separate calls.

        D5 §B1: "list(session) → [WorkerState]" is satisfied by calling observe()
        over each handle in the session's active-worker set.  A list() helper
        may be added at the implementation level; it is not part of this contract
        because thought/orchestration iterate over handles they already hold.
        """
        ...

    # ------------------------------------------------------------------
    # cancel — D5 §B1/§B4 graceful then hard
    # ------------------------------------------------------------------

    async def cancel(
        self,
        worker_id: UUID,
        reason: str,
    ) -> None:
        """
        Stop a worker: cooperative cancellation first, hard kill after grace
        window (D5 §B4 graceWindow = 20 s, SIMPLEST: implementation default).

        Steps (D5 §B4):
        1. Emit 'cancel' signal to the worker (cooperative stop at turn end).
        2. Wait up to graceWindow; if still running, hard-terminate via the
           host daemon.
        3. Release the write-scope claim unconditionally.
        4. Mark dispatch ledger row state='killed', reason=reason.
        5. Emit worker.failed event to the event stream (thought runs recovery).

        Callers: thought's recovery ladder (A4), parent-session cascade-kill,
        non-progress watchdog escalation path, guardrail trip (G4).
        """
        ...

    # ------------------------------------------------------------------
    # suspend / resume — pause-aware TTL (D-029)
    # ------------------------------------------------------------------

    async def suspend(
        self,
        handle: WorkerHandle,
        reason: str = "",
    ) -> None:
        """
        Cooperatively suspend a running worker and freeze its claim TTL.

        Pause-aware TTL (D-029): a suspended worker's claim does not expire
        while suspended; the claim TTL resumes counting from where it left off
        when the worker is resumed.  This prevents claim expiry during planned
        pauses (e.g. session pause, node maintenance).

        The worker transitions to SUSPENDED state.  The event stream emits a
        SUSPENDED event.  The worker's runtime unit is quiesced (told to stop
        accepting new turns) but its state is preserved for resume.

        SIMPLEST: suspend/resume are cooperative; the conductor signals the
        worker and updates the dispatch ledger.  Hard suspension (SIGSTOP
        equivalent) is a post-VS0 enhancement.
        """
        ...

    async def resume(
        self,
        handle: WorkerHandle,
    ) -> None:
        """
        Resume a previously suspended worker and unfreeze its claim TTL.

        The worker transitions from SUSPENDED back to RUNNING.  The claim TTL
        is reset to the default for its lane (D5 §C2).  The event stream emits
        a RESUMED event.
        """
        ...

    # ------------------------------------------------------------------
    # register_claims_hook — claim-on-first-write callback (D-029)
    # ------------------------------------------------------------------

    def register_claims_hook(
        self,
        hook: "ClaimsHookCallable",
    ) -> None:
        """
        Register a callback invoked whenever a claim conflict is detected.

        Called on two occasions:
        1. At assign() time if spec.write_scope is not None and the claim cannot
           be acquired (conflict with a holder).
        2. At the first blackboard_write() by a worker whose spec.write_scope
           was None (claim-on-first-write, D-029) — if that registration fails.

        The hook receives (handle, ClaimConflict).  The thought role uses this
        to feed the conflict into its serialization / re-scope / merge ladder
        (D5 §C3) without polling.

        SIMPLEST: one hook per conductor instance; re-registering replaces the
        previous hook.  Multiple hooks (fan-out) are an implementation extension.
        """
        ...

    # ------------------------------------------------------------------
    # register_nonprogress_hook — non-progress escalation callback (D5 §D)
    # ------------------------------------------------------------------

    def register_nonprogress_hook(
        self,
        hook: "NonProgressHookCallable",
    ) -> None:
        """
        Register a callback invoked when the non-progress watchdog fires.

        The watchdog fires when consecutive_no_progress_turns >= K (D5 §D-II;
        proposed default K=3, OPEN).  The hook receives
        (handle, consecutive_no_progress_turns: int).

        The thought role uses this to enter the recovery ladder (D5 §A4):
        bounce-back → re-route → re-decompose → spawn review → escalate to user.

        Escalation, not hard kill, is the default action (D5 §D-II PROPOSED):
        the hook drives thought to make the quality decision; the conductor only
        signals.

        SIMPLEST: one hook per conductor instance; re-registering replaces the
        previous hook.
        """
        ...


# ---------------------------------------------------------------------------
# Callable type aliases for the hook signatures (runtime-annotated versions
# used in register_* signatures above).  These are separated here to avoid
# circular forward-reference issues with the string-quoted forms in the class.
# ---------------------------------------------------------------------------

from collections.abc import Callable  # noqa: E402 — stdlib, after Protocol body

ClaimsHookCallable = Callable[[WorkerHandle, ClaimConflict], None]
"""
Callable type for the claims-conflict hook (D-029, D5 §C3).
Signature: (handle: WorkerHandle, conflict: ClaimConflict) -> None.
Registered via WorkerLifecycle.register_claims_hook().
"""

NonProgressHookCallable = Callable[[WorkerHandle, int], None]
"""
Callable type for the non-progress-watchdog hook (D5 §D-II).
Signature: (handle: WorkerHandle, consecutive_no_progress_turns: int) -> None.
Registered via WorkerLifecycle.register_nonprogress_hook().
"""

WorkerSpec = WorkSpec
"""Alias for WorkSpec — satisfies import sites that use the 'WorkerSpec' name."""

__all__ = [
    # Types
    "ClaimConflict",
    "ClaimScope",
    "LogicalScope",
    "PathGlobScope",
    "ProgressSignal",
    "TaskScope",
    "WorkSpec",
    "WorkerSpec",  # alias
    "WorkerEvent",
    "WorkerEventKind",
    "WorkerHandle",
    "WorkerLifecycle",
    "WorkerStatus",
    "WorkerStatusCode",
    # Hook callable type aliases — canonical names for callers
    "ClaimsHookCallable",
    "NonProgressHookCallable",
]
