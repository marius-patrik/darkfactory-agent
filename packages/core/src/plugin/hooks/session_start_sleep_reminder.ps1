$ErrorActionPreference = "Stop"

try {
    $memoryRoot = Join-Path $env:USERPROFILE ".codex\memories"
    $statePath = Join-Path $memoryRoot ".sleep-state.json"
    $now = Get-Date
    $reasons = New-Object System.Collections.Generic.List[string]

    $lastSleep = $null
    if (Test-Path -LiteralPath $statePath) {
        try {
            $state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
            if ($state.last_sleep) {
                $lastSleep = [datetime]$state.last_sleep
            }
        } catch {
            $reasons.Add("sleep state marker is unreadable")
        }
    } else {
        $reasons.Add("sleep state marker is missing")
    }

    if ($lastSleep -and (($now - $lastSleep).TotalDays -gt 14)) {
        $reasons.Add(("last sleep was {0:N0} days ago" -f (($now - $lastSleep).TotalDays)))
    }

    function Test-ActiveMemoryFile {
        param(
            [Parameter(Mandatory=$true)][string]$Path,
            [Parameter(Mandatory=$true)][string]$EmptyPattern,
            [Parameter(Mandatory=$true)][string]$Label
        )

        if (-not (Test-Path -LiteralPath $Path)) {
            return
        }

        $content = Get-Content -LiteralPath $Path -Raw
        if ($content -notmatch $EmptyPattern) {
            $ageHours = ($now - (Get-Item -LiteralPath $Path).LastWriteTime).TotalHours
            if ($ageHours -gt 48) {
                $reasons.Add(("{0} has active content older than 48 hours" -f $Label))
            }
        }
    }

    Test-ActiveMemoryFile -Path (Join-Path $memoryRoot "cache.md") -EmptyPattern "Current cache:\s*-\s*None\." -Label "cache.md"
    Test-ActiveMemoryFile -Path (Join-Path $memoryRoot "SHORT.md") -EmptyPattern "Current entries:\s*-\s*None\." -Label "SHORT.md"

    $notesDir = Join-Path $memoryRoot "extensions\ad_hoc\notes"
    if ((Test-Path -LiteralPath $notesDir) -and $lastSleep) {
        $newNotes = Get-ChildItem -LiteralPath $notesDir -Filter "*.md" -File |
            Where-Object { $_.LastWriteTime -gt $lastSleep } |
            Select-Object -First 1
        if ($newNotes) {
            $reasons.Add("new ad-hoc memory notes appeared after the last sleep")
        }
    } elseif (Test-Path -LiteralPath $notesDir) {
        $hasNotes = Get-ChildItem -LiteralPath $notesDir -Filter "*.md" -File | Select-Object -First 1
        if ($hasNotes) {
            $reasons.Add("ad-hoc memory notes exist and no sleep marker is present")
        }
    }

    if ($reasons.Count -eq 0) {
        $output = @{ continue = $true }
    } else {
        $reasonText = ($reasons | Select-Object -Unique) -join "; "
        $additionalContext = "Rommie sleep reminder: memory may be stale ($reasonText). When the user's task allows it, suggest running `/prompts:sleep` or explicitly ask Codex to use `$sleep` to consolidate long-term memory and reduce context rot."
        $output = @{
            continue = $true
            hookSpecificOutput = @{
                hookEventName = "SessionStart"
                additionalContext = $additionalContext
            }
        }
    }

    $output | ConvertTo-Json -Depth 5 -Compress
} catch {
    @{
        continue = $true
        hookSpecificOutput = @{
            hookEventName = "SessionStart"
            additionalContext = "Rommie sleep reminder hook failed: $($_.Exception.Message)"
        }
    } | ConvertTo-Json -Depth 5 -Compress
}

