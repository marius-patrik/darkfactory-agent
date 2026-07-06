"""Execution lane contract module.

Defines the interface, data structures, and registry for executing detached
and inline capabilities in the Andromeda runtime.
"""

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Iterator, Literal, Protocol, runtime_checkable

# Lane registry constants
LANE_DAEMON_INLINE = "daemon-inline"
LANE_K3S_JOB = "k3s-job"
LANE_KNATIVE = "knative"


@dataclass(frozen=True)
class ExecSpec:
    """Specification of a task to be executed in an execution lane.

    Attributes:
        command: List of command arguments to execute, or None if executing an image default.
        image: Container image name to run, or None for host-bound/inline execution.
        args: Optional arguments appended to command/image entrypoint.
        env: Environment variables. MUST contain secret references only (e.g., 'secret:NAME'),
            never plaintext secret values.
        mounts: Directory/file mounts to bind. Recommended format: ['/host/path:/container/path'].
        working_dir: The working directory for execution inside the environment.
        timeout: Execution timeout in seconds.
        resource_hints: Resource suggestions (e.g., {'cpu': '2', 'memory': '4Gi', 'gpu': '1'}).
        lane: Execution lane hint, specifying inline (hot loop, daemon) vs detached (isolated job/service).
        isolation_level: Requested isolation environment.
    """

    command: list[str] | None = None
    image: str | None = None
    args: list[str] | None = None
    env: dict[str, str] = field(default_factory=dict)
    mounts: list[str] = field(default_factory=list)
    working_dir: str | None = None
    timeout: float | None = None
    resource_hints: dict[str, str] = field(default_factory=dict)
    lane: Literal["inline", "detached"] | None = None
    isolation_level: Literal["host", "container", "sandbox"] | None = None


@dataclass(frozen=True)
class ExecHandle:
    """A handle tracking a submitted task execution.

    Attributes:
        id: Unique execution identifier.
        lane: Name of the execution lane handling the task.
        submitted_at: Datetime when the execution was submitted.
    """

    id: str
    lane: str
    submitted_at: datetime


@dataclass(frozen=True)
class ExecStatus:
    """The current execution status of a submitted task.

    Attributes:
        status: The execution state (queued, running, succeeded, failed, or cancelled).
        exit_code: Process exit code if completed (succeeded/failed), else None.
        error_message: Detailed error message if execution failed, else None.
        finished_at: Datetime when the execution finished, else None.
    """

    status: Literal["queued", "running", "succeeded", "failed", "cancelled"]
    exit_code: int | None = None
    error_message: str | None = None
    finished_at: datetime | None = None


@runtime_checkable
class ExecLane(Protocol):
    """The protocol defining the interface for an execution lane."""

    def submit(self, spec: ExecSpec) -> ExecHandle:
        """Submit a task for execution.

        Args:
            spec: The execution specification.

        Returns:
            An execution handle tracking the task.
        """
        ...

    def status(self, handle: ExecHandle) -> ExecStatus:
        """Get the current status of the execution.

        Args:
            handle: The execution handle.

        Returns:
            The execution status.
        """
        ...

    def logs(self, handle: ExecHandle) -> Iterator[str]:
        """Access the logs of the execution as a line iterator.

        Args:
            handle: The execution handle.

        Returns:
            An iterator yielding log lines.
        """
        ...

    def cancel(self, handle: ExecHandle) -> None:
        """Cancel an in-flight execution.

        Args:
            handle: The execution handle.
        """
        ...

    def capabilities(self) -> dict[str, Any]:
        """Retrieve capabilities of this execution lane.

        Returns:
            A dictionary describing the capabilities, such as support for
            container isolation, resource allocation, and scaling.
        """
        ...


class KnativeExecLane(ExecLane):
    """Knative execution lane stub.

    Reserved for post-4.0. Currently raises NotImplementedError for all operations,
    acting as the integration seam.
    """

    def submit(self, spec: ExecSpec) -> ExecHandle:
        raise NotImplementedError("Knative execution lane is reserved for post-4.0.")

    def status(self, handle: ExecHandle) -> ExecStatus:
        raise NotImplementedError("Knative execution lane is reserved for post-4.0.")

    def logs(self, handle: ExecHandle) -> Iterator[str]:
        raise NotImplementedError("Knative execution lane is reserved for post-4.0.")

    def cancel(self, handle: ExecHandle) -> None:
        raise NotImplementedError("Knative execution lane is reserved for post-4.0.")

    def capabilities(self) -> dict[str, Any]:
        raise NotImplementedError("Knative execution lane is reserved for post-4.0.")


# Runtime Registry
_registry: dict[str, ExecLane] = {
    LANE_KNATIVE: KnativeExecLane(),
}


def register_lane(name: str, lane: ExecLane) -> None:
    """Register an execution lane implementation.

    Args:
        name: Name of the execution lane (e.g., 'daemon-inline', 'k3s-job').
        lane: An instance implementing the ExecLane protocol.
    """
    _registry[name] = lane


def get_lane(name: str) -> ExecLane:
    """Retrieve a registered execution lane by name.

    Args:
        name: Name of the execution lane.

    Returns:
        The registered ExecLane instance.

    Raises:
        NotImplementedError: If the lane is one of the standard lanes but has
            not yet been registered at runtime.
        KeyError: If the lane name is unknown.
    """
    if name not in _registry:
        if name in (LANE_DAEMON_INLINE, LANE_K3S_JOB):
            raise NotImplementedError(
                f"Execution lane '{name}' is not implemented at the contract level. "
                "Concrete implementation must be registered at runtime."
            )
        raise KeyError(f"Execution lane '{name}' not found in registry.")
    return _registry[name]
