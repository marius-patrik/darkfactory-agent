$ErrorActionPreference = "Stop"

try {
    $memoryRoot = Join-Path $env:USERPROFILE ".codex\memories"
    $statePath = Join-Path $memoryRoot ".breathe-state.json"
    $last = "no prior breathe checkpoint"

    if (Test-Path -LiteralPath $statePath) {
        try {
            $state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
            if ($state.last_breathe) {
                $last = "last breathe checkpoint: $($state.last_breathe); state: $($state.state)"
            }
        } catch {
            $last = "breathe checkpoint exists but is unreadable"
        }
    }

    @{
        continue = $true
        hookSpecificOutput = @{
            hookEventName = "SessionStart"
            additionalContext = "Rommie breathe hook ($last). Before final answers: re-check the newest user request, inspect live repo/process/test state, update memory when state changed, and continue if required work remains. When work is truly done, run or suggest `$sleep` if memory is stale and remind the user to compact large threads."
        }
    } | ConvertTo-Json -Depth 5 -Compress
} catch {
    @{
        continue = $true
        hookSpecificOutput = @{
            hookEventName = "SessionStart"
            additionalContext = "Rommie breathe hook failed: $($_.Exception.Message)"
        }
    } | ConvertTo-Json -Depth 5 -Compress
}

