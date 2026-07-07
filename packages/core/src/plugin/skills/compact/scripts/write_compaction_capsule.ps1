param(
    [string]$MemoryRoot = (Join-Path $env:USERPROFILE ".codex\memories"),
    [Parameter(Mandatory=$true)][string]$Objective,
    [Parameter(Mandatory=$true)][string]$State,
    [Parameter(Mandatory=$true)][string]$Next,
    [string]$Validation = "Not recorded.",
    [string]$Blockers = "None.",
    [string]$Repos = "Not recorded.",
    [switch]$ClearCache
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $MemoryRoot)) {
    New-Item -ItemType Directory -Path $MemoryRoot -Force | Out-Null
}

$now = Get-Date -Format o
$handoffPath = Join-Path $MemoryRoot "handoff.md"
$shortPath = Join-Path $MemoryRoot "SHORT.md"
$cachePath = Join-Path $MemoryRoot "cache.md"
$statePath = Join-Path $MemoryRoot ".compact-state.json"

if (-not (Test-Path -LiteralPath $handoffPath)) {
    "# Handoff`r`n" | Set-Content -LiteralPath $handoffPath -Encoding UTF8
}
if (-not (Test-Path -LiteralPath $shortPath)) {
    "# Short-Term Multi-Session Memory`r`n`r`nCurrent entries:`r`n- None.`r`n" | Set-Content -LiteralPath $shortPath -Encoding UTF8
}
if (-not (Test-Path -LiteralPath $cachePath)) {
    "# Immediate Task Cache`r`n`r`nCurrent cache:`r`n- None.`r`n" | Set-Content -LiteralPath $cachePath -Encoding UTF8
}

$payload = [ordered]@{
    last_compact = $now
    objective = $Objective
    state = $State
    next = $Next
    validation = $Validation
    blockers = $Blockers
    repos = $Repos
}
$payload | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $statePath -Encoding UTF8

function Update-Block {
    param(
        [Parameter(Mandatory=$true)][string]$Path,
        [Parameter(Mandatory=$true)][string]$Start,
        [Parameter(Mandatory=$true)][string]$End,
        [Parameter(Mandatory=$true)][string]$Section
    )
    $existing = Get-Content -LiteralPath $Path -Raw
    $pattern = "(?s)" + [regex]::Escape($Start) + ".*?" + [regex]::Escape($End)
    if ($existing -match $pattern) {
        $updated = [regex]::Replace($existing, $pattern, [System.Text.RegularExpressions.MatchEvaluator]{ param($m) $Section })
    } else {
        $updated = $existing.TrimEnd() + "`r`n`r`n" + $Section + "`r`n"
    }
    Set-Content -LiteralPath $Path -Value $updated -Encoding UTF8
}

$handoffStart = "<!-- rommie:compact:start -->"
$handoffEnd = "<!-- rommie:compact:end -->"
$handoffSection = @"
$handoffStart
## Rommie Compaction Capsule
Updated: $now

Objective:
- $Objective

State:
- $State

Next:
- $Next

Validation:
- $Validation

Blockers:
- $Blockers

Repos:
- $Repos
$handoffEnd
"@
Update-Block -Path $handoffPath -Start $handoffStart -End $handoffEnd -Section $handoffSection

$shortStart = "<!-- rommie:compact-short:start -->"
$shortEnd = "<!-- rommie:compact-short:end -->"
$shortSection = @"
$shortStart
## Rommie Compaction Active Work
Updated: $now
Status: $State
Current objective: $Objective
Next actions: $Next
Blockers: $Blockers
Last validation: $Validation
$shortEnd
"@
Update-Block -Path $shortPath -Start $shortStart -End $shortEnd -Section $shortSection

if ($ClearCache) {
    $cache = @"
# Immediate Task Cache

Use this file for the current task's volatile facts.

Current cache:
- None.
"@
    Set-Content -LiteralPath $cachePath -Value $cache -Encoding UTF8
}

Write-Output "Compaction capsule updated: $statePath"

