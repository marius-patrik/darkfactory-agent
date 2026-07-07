$ErrorActionPreference = "Stop"

try {
    $memoryRoot = Join-Path $env:USERPROFILE ".codex\memories"
    $handoffPath = Join-Path $memoryRoot "handoff.md"
    $statePath = Join-Path $memoryRoot ".breathe-state.json"

    if (-not (Test-Path -LiteralPath $memoryRoot)) {
        New-Item -ItemType Directory -Path $memoryRoot -Force | Out-Null
    }
    if (-not (Test-Path -LiteralPath $handoffPath)) {
        "# Handoff`r`n" | Set-Content -LiteralPath $handoffPath -Encoding UTF8
    }

    $now = Get-Date -Format o
    $cwd = (Get-Location).Path
    [ordered]@{
        last_stop_reflection = $now
        cwd = $cwd
        reminder = "Continue if work remains; if truly done, sleep when stale and remind user to compact."
    } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $statePath -Encoding UTF8

    $begin = "<!-- rommie:breathe-hook:start -->"
    $end = "<!-- rommie:breathe-hook:end -->"
    $section = @"
$begin
## Rommie Breathe Hook
Updated: $now
CWD: ``$cwd``

Stop reflection:
- If the last response did not finish required work, resume and continue.
- Keep `cache.md`, `SHORT.md`, and `handoff.md` current for active work.
- When work is truly complete, run or suggest `$sleep` when memory is stale and remind the user to compact large threads.
$end
"@

    $existing = Get-Content -LiteralPath $handoffPath -Raw
    $pattern = "(?s)<!-- rommie:breathe-hook:start -->.*?<!-- rommie:breathe-hook:end -->"
    if ($existing -match $pattern) {
        $updated = [regex]::Replace($existing, $pattern, [System.Text.RegularExpressions.MatchEvaluator]{ param($m) $section })
    } else {
        $updated = $existing.TrimEnd() + "`r`n`r`n" + $section + "`r`n"
    }
    Set-Content -LiteralPath $handoffPath -Value $updated -Encoding UTF8
} catch {
    $logPath = Join-Path $env:USERPROFILE ".codex\hooks\stop_breathe_reflection.error.log"
    "[$(Get-Date -Format o)] $($_.Exception.Message)" | Add-Content -LiteralPath $logPath -Encoding UTF8
}

