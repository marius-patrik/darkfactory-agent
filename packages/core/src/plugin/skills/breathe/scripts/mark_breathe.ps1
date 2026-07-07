param(
    [string]$MemoryRoot = (Join-Path $env:USERPROFILE ".codex\memories"),
    [string]$Summary = "Breathe checkpoint recorded.",
    [string]$State = "in-progress",
    [string]$Next = "Re-check live state and continue if work remains."
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $MemoryRoot)) {
    New-Item -ItemType Directory -Path $MemoryRoot -Force | Out-Null
}

$now = Get-Date -Format o
$handoffPath = Join-Path $MemoryRoot "handoff.md"
$statePath = Join-Path $MemoryRoot ".breathe-state.json"

if (-not (Test-Path -LiteralPath $handoffPath)) {
    "# Handoff`r`n" | Set-Content -LiteralPath $handoffPath -Encoding UTF8
}

$payload = [ordered]@{
    last_breathe = $now
    state = $State
    summary = $Summary
    next = $Next
}
$payload | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $statePath -Encoding UTF8

$begin = "<!-- rommie:breathe:start -->"
$end = "<!-- rommie:breathe:end -->"
$section = @"
$begin
## Rommie Breathe Checkpoint
Updated: $now
State: $State

Summary:
- $Summary

Next:
- $Next

Reflection:
- Continue working if required work remains and can be advanced.
- If truly done, run or suggest `$sleep` when memory is stale and remind the user to compact large threads.
$end
"@

$existing = Get-Content -LiteralPath $handoffPath -Raw
$pattern = "(?s)<!-- rommie:breathe:start -->.*?<!-- rommie:breathe:end -->"
if ($existing -match $pattern) {
    $updated = [regex]::Replace($existing, $pattern, [System.Text.RegularExpressions.MatchEvaluator]{ param($m) $section })
} else {
    $updated = $existing.TrimEnd() + "`r`n`r`n" + $section + "`r`n"
}
Set-Content -LiteralPath $handoffPath -Value $updated -Encoding UTF8

Write-Output "Breathe checkpoint updated: $statePath"

