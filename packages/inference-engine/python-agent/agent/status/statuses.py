"""Status vocabulary + proto/PostgreSQL alignment.

This module defines the canonical eight *result* states for runs, tasks and
rolled-up parents.  It intentionally excludes the two *liveness* states
(``RUN_STATUS_RUNNING`` / ``RUN_STATUS_PAUSED``) from the wire enum: the
vocabulary is about **result**, not heartbeat or claim liveness
(see ``.plans/design/drafts/D6-status-machine.md`` §3.1).

PostgreSQL reconciliation (issue #1276)
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

The Postgres ``run_status_value`` enum and the ``StatusValue`` enum below
share the exact same string names.  ``PG_RUN_STATUS_VALUES`` documents and
locks that ordering so drift is caught by tests.

The protobuf ``RunStatus`` enum (``proto/rommie/v1/common.proto``) has ten
members.  Values ``1..8`` map to the eight result states below.  Values
``0`` (``RUN_STATUS_UNSPECIFIED``), ``9`` (``RUN_STATUS_RUNNING``) and
``10`` (``RUN_STATUS_PAUSED``) are rejected by ``from_proto`` because they
are either unspecified or liveness states, not result states.
"""

from __future__ import annotations

from enum import Enum


class StatusValue(str, Enum):
    """Canonical no-false-green result states.

    Attributes:
        useful_result: Acceptance-check passed; a validated artifact/proof exists.
        no_artifact: Run ended but produced no artifact to point at.
        missing_evidence: Artifact exists but required proof is insufficient.
        unresolved: Work is genuinely incomplete / open / parked.
        blocked: Stalled on an external dependency; carries an ``unblock_from``.
        failed: Hard error / acceptance impossible / contract violation.
        released: ``useful_result`` whose claims have been released and integrated.
        expired: Claim or run TTL lapsed before reaching a success state.
    """

    useful_result = "useful_result"
    no_artifact = "no_artifact"
    missing_evidence = "missing_evidence"
    unresolved = "unresolved"
    blocked = "blocked"
    failed = "failed"
    released = "released"
    expired = "expired"

    def is_terminal(self) -> bool:
        """Return True for terminal-negative or terminal-post-success states."""
        return self in (StatusValue.failed, StatusValue.released, StatusValue.expired)

    def is_success(self) -> bool:
        """Return True for green/success-ish states."""
        return self in (StatusValue.useful_result, StatusValue.released)

    def is_recoverable(self) -> bool:
        """Return True for non-terminal, resumable states."""
        return self in (
            StatusValue.no_artifact,
            StatusValue.missing_evidence,
            StatusValue.unresolved,
            StatusValue.blocked,
        )

    def state_class(self) -> str:
        """Return the UX/policy class for this state.

        Returns:
            One of ``success``, ``not-done-yet``, ``stalled``,
            ``terminal-neg``, ``post-success``.
        """
        return _STATE_CLASS[self]


_STATE_CLASS: dict[StatusValue, str] = {
    StatusValue.useful_result: "success",
    StatusValue.no_artifact: "not-done-yet",
    StatusValue.missing_evidence: "not-done-yet",
    StatusValue.unresolved: "not-done-yet",
    StatusValue.blocked: "stalled",
    StatusValue.failed: "terminal-neg",
    StatusValue.released: "post-success",
    StatusValue.expired: "terminal-neg",
}

# PostgreSQL enum ``run_status_value`` literal names, in proto order.
# This constant guards issue #1276: the PG enum string values must match
# ``StatusValue.value`` exactly.
PG_RUN_STATUS_VALUES: list[str] = [
    "useful_result",
    "no_artifact",
    "missing_evidence",
    "unresolved",
    "blocked",
    "failed",
    "released",
    "expired",
]

# Proto value (1..8) for each StatusValue, in the order defined by
# ``proto/rommie/v1/common.proto``.
_PROTO_VALUE: dict[StatusValue, int] = {
    StatusValue.useful_result: 1,
    StatusValue.no_artifact: 2,
    StatusValue.missing_evidence: 3,
    StatusValue.unresolved: 4,
    StatusValue.blocked: 5,
    StatusValue.failed: 6,
    StatusValue.released: 7,
    StatusValue.expired: 8,
}

_STATUS_BY_PROTO: dict[int, StatusValue] = {v: k for k, v in _PROTO_VALUE.items()}


def to_proto(status: StatusValue) -> int:
    """Map a result state to its protobuf integer value (1..8).

    Args:
        status: A result state.

    Returns:
        The protobuf wire value.

    Raises:
        ValueError: If ``status`` is not a valid result state.
    """
    try:
        return _PROTO_VALUE[status]
    except KeyError as exc:
        raise ValueError(f"{status!r} is not a valid result StatusValue") from exc


def from_proto(value: int) -> StatusValue:
    """Map a protobuf integer value to a result state.

    Args:
        value: A protobuf wire value.

    Returns:
        The corresponding ``StatusValue``.

    Raises:
        ValueError: If ``value`` is 0 (unspecified), 9 (running) or 10 (paused).
            Those values are not result states.
    """
    if value == 0:
        raise ValueError("proto value 0 (RUN_STATUS_UNSPECIFIED) is not a result state")
    if value == 9:
        raise ValueError("proto value 9 (RUN_STATUS_RUNNING) is a liveness state, not a result state")
    if value == 10:
        raise ValueError("proto value 10 (RUN_STATUS_PAUSED) is a liveness state, not a result state")
    try:
        return _STATUS_BY_PROTO[value]
    except KeyError as exc:
        raise ValueError(f"proto value {value} is not a known result state") from exc
