<#
.SYNOPSIS
  End-to-end check on Windows: setup, made-up data, backup, a deleted file,
  restore, comparison of hashes, update, uninstall.

.DESCRIPTION
  Works on a fake profile in a temporary folder, with real restic and rclone,
  the real Task Scheduler, the real key store and the real PATH of the user.
  In CI that is a clean machine. On a computer that has a real installation
  pass -Sandbox <name>: the scheduler folder, the key store group and the PATH
  key then carry that name and the real ones are not touched.

.PARAMETER Exe
  The built backupclaude.exe.

.PARAMETER Sandbox
  A short name (letters and digits) for a sandbox. Required outside CI.
#>
param(
  [Parameter(Mandatory = $true)][string]$Exe,
  [string]$Sandbox = '',
  [switch]$KeepWork
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$OutputEncoding = [Text.Encoding]::UTF8

if (-not $env:GITHUB_ACTIONS -and -not $Sandbox) {
  throw 'Outside CI this script needs -Sandbox <name>, so that a real installation is not touched.'
}
if ($Sandbox) { $env:BACKUPCLAUDE_SANDBOX = $Sandbox }
$Exe = (Resolve-Path $Exe).Path

$script:failures = New-Object System.Collections.ArrayList
function Step([string]$text) { Write-Host ''; Write-Host "== $text" }
function Check([bool]$condition, [string]$what) {
  if ($condition) { Write-Host "  ok    $what" }
  else { Write-Host "  FAIL  $what"; [void]$script:failures.Add($what) }
}

# Runs the program and returns the parsed JSON answer and the exit code.
function Invoke-App([string]$program, [string[]]$arguments, [string]$stdin = $null) {
  $stderrFile = Join-Path $work ('stderr-' + [guid]::NewGuid().ToString('N') + '.txt')
  if ($null -ne $stdin) { $text = $stdin | & $program @arguments 2>$stderrFile }
  else { $text = & $program @arguments 2>$stderrFile }
  $code = $LASTEXITCODE
  $raw = ($text | Out-String)
  $parsed = $null
  try { $parsed = $raw | ConvertFrom-Json } catch { }
  if ($null -eq $parsed -or $code -ne 0) {
    Write-Host "  --- $($arguments -join ' ') exited with $code"
    Write-Host $raw
    if (Test-Path $stderrFile) { Get-Content $stderrFile | ForEach-Object { Write-Host "  stderr: $_" } }
  }
  return [pscustomobject]@{ Code = $code; Json = $parsed; Raw = $raw }
}

# Runs a program whose messages on stderr must not stop this script.
function Invoke-Quiet([string]$program, [string[]]$arguments) {
  $previous = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try { return (& $program @arguments 2>&1) } finally { $ErrorActionPreference = $previous }
}

# True when the task is in the Task Scheduler.
function Test-Task([string]$name) {
  $previous = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    & schtasks.exe /Query /TN "\BackUpClaude$suffix\$name" 2>&1 | Out-Null
    return ($LASTEXITCODE -eq 0)
  } finally { $ErrorActionPreference = $previous }
}

function Sha([string]$path) { (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash }

$suffix = ''
if ($Sandbox) { $suffix = "-$Sandbox" }
$work = Join-Path ([IO.Path]::GetTempPath()) ('bc-e2e-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
$profileDir = Join-Path $work 'profile'
New-Item -ItemType Directory -Force $profileDir | Out-Null

# The program works in a fake profile: the home folder, the data of the
# application and Claude's folder all lie inside it.
$env:USERPROFILE = $profileDir
$env:APPDATA = Join-Path $profileDir 'AppData\Roaming'
$env:LOCALAPPDATA = Join-Path $profileDir 'AppData\Local'
Remove-Item Env:BACKUPCLAUDE_HOME -ErrorAction SilentlyContinue
$dataDir = Join-Path $profileDir '.backupclaude'
$binDir = Join-Path $dataDir 'bin'
$installed = Join-Path $binDir 'backupclaude.exe'
$repoDir = Join-Path $work 'ClaudeBackup\repo'

# A name with Cyrillic and spaces, built without putting Cyrillic into the file.
$cyr = -join ([char[]](0x41F, 0x440, 0x43E, 0x435, 0x43A, 0x442))
$projectDir = Join-Path $profileDir "Projects\$cyr demo"
$sessionA = [guid]::NewGuid().ToString()
$sessionB = [guid]::NewGuid().ToString()
$transcriptDir = Join-Path $profileDir '.claude\projects\-e2e-demo'

Step 'Made-up data'
New-Item -ItemType Directory -Force $transcriptDir, (Join-Path $projectDir 'src'), (Join-Path $profileDir '.claude\skills') | Out-Null
foreach ($id in @($sessionA, $sessionB)) {
  $line = @{ type = 'user'; cwd = $projectDir; sessionId = $id; message = @{ role = 'user'; content = "hello from $id" } } | ConvertTo-Json -Compress
  $reply = @{ type = 'assistant'; cwd = $projectDir; sessionId = $id; message = @{ role = 'assistant'; content = 'hello back' } } | ConvertTo-Json -Compress
  [IO.File]::WriteAllText((Join-Path $transcriptDir "$id.jsonl"), "$line`n$reply`n", (New-Object Text.UTF8Encoding($false)))
}
[IO.File]::WriteAllText((Join-Path $profileDir '.claude\settings.json'), '{"theme":"dark"}', (New-Object Text.UTF8Encoding($false)))
[IO.File]::WriteAllText((Join-Path $profileDir '.claude.json'), '{"numStartups":1}', (New-Object Text.UTF8Encoding($false)))
[IO.File]::WriteAllText((Join-Path $projectDir 'notes.txt'), "notes of the project`nsecond line`n", (New-Object Text.UTF8Encoding($false)))
$random = New-Object byte[] (1MB)
(New-Object Random 42).NextBytes($random)
[IO.File]::WriteAllBytes((Join-Path $projectDir 'src\data.bin'), $random)
$sessionFile = Join-Path $transcriptDir "$sessionA.jsonl"
$notesFile = Join-Path $projectDir 'notes.txt'
$binFile = Join-Path $projectDir 'src\data.bin'
$hashSession = Sha $sessionFile
$hashNotes = Sha $notesFile
$hashBin = Sha $binFile
Check (Test-Path $sessionFile) 'the made-up session exists'

Step 'install.ps1 from a local archive'
$zip = Join-Path $work 'backupclaude_0.0.0_windows_amd64.zip'
$stageDir = Join-Path $work 'stage'
New-Item -ItemType Directory -Force $stageDir | Out-Null
$stagedExe = Join-Path $stageDir 'backupclaude.exe'
Copy-Item -LiteralPath $Exe -Destination $stagedExe
Compress-Archive -LiteralPath $stagedExe -DestinationPath $zip -Force
$zipHash = Sha $zip
$installScript = Join-Path $PSScriptRoot 'install.ps1'
$badHash = Invoke-Quiet powershell.exe @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $installScript, '-FromFile', $zip, '-Sha256', ('0' * 64), '-NoSetup')
Check ($LASTEXITCODE -ne 0 -and -not (Test-Path $installed)) 'a wrong SHA-256 installs nothing'
$ps = Invoke-Quiet powershell.exe @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $installScript, '-FromFile', $zip, '-Sha256', $zipHash, '-NoSetup')
Check ($LASTEXITCODE -eq 0) 'install.ps1 finished'
if ($LASTEXITCODE -ne 0) { $ps | ForEach-Object { Write-Host "  $_" } }
Check (Test-Path $installed) 'install.ps1 put the program into the data folder'

Step 'setup (first run, no questions)'
$r = Invoke-App $installed @('setup', '--yes', '--json', '--local', $repoDir, '--skill', '--projects-now', '--lang', 'en')
Check ($r.Code -eq 0 -and $r.Json.ok -eq $true) 'setup finished with exit code 0'
if ($r.Json) {
  $bad = @($r.Json.data.steps | Where-Object { $_.status -eq 'fail' })
  Check ($bad.Count -eq 0) 'no step of setup failed'
  Check ($r.Json.data.password.created -eq $true -and $r.Json.data.password.stored -eq $true) 'a password was created in the key store'
  Check (@($r.Json.data.snapshots).Count -eq 2) 'two first snapshots were taken (claude and projects)'
  Check ($r.Raw -notmatch '[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){7}') 'the answer holds no password'
}
Check (Test-Path $installed) 'the program is installed in the data folder'
Check (Test-Path (Join-Path $binDir 'restic-*\restic.exe')) 'restic is installed'
Check (Test-Path (Join-Path $binDir 'rclone-*\rclone.exe')) 'rclone is installed'
Check (Test-Path (Join-Path $repoDir 'config')) 'the repository exists'
Check (Test-Path (Join-Path $profileDir '.claude\skills\backup\SKILL.md')) 'the /backup skill is installed'

Step 'PATH, tasks'
if ($Sandbox) { $envKey = "HKCU:\Software\BackUpClaude$suffix\Environment" } else { $envKey = 'HKCU:\Environment' }
$userPath = (Get-ItemProperty -Path $envKey -Name Path -ErrorAction SilentlyContinue).Path
Check ($userPath -and ($userPath -split ';' | Where-Object { $_.TrimEnd('\') -ieq $binDir })) 'the program folder is in the PATH of the user'
$s = Invoke-App $installed @('schedule', 'status', '--json')
$jobs = @($s.Json.data.jobs)
Check ($jobs.Count -eq 6 -and @($jobs | Where-Object { $_.installed -and $_.in_sync }).Count -eq 6) 'six tasks are installed and match the settings'
foreach ($name in 'claude', 'projects', 'cleanup', 'check', 'restore-test', 'summary') {
  Check (Test-Task $name) "the task $name is in the Task Scheduler"
}

Step 'setup again: nothing changes'
$configFile = Join-Path $dataDir 'config.json'
$configBefore = Sha $configFile
$r2 = Invoke-App $installed @('setup', '--yes', '--json')
Check ($r2.Code -eq 0 -and $r2.Json.data.rerun -eq $true -and $r2.Json.data.config_changed -eq $false) 'a rerun keeps the settings'
Check ((Sha $configFile) -eq $configBefore) 'config.json is byte for byte the same'

Step 'backup after a change'
Add-Content -Path (Join-Path $transcriptDir "$sessionB.jsonl") -Value '{"type":"user","message":{"role":"user","content":"one more"}}' -Encoding UTF8
$b = Invoke-App $installed @('backup', '--json')
Check ($b.Code -eq 0 -and $b.Json.ok -eq $true) 'backup finished'
$l = Invoke-App $installed @('list', '--snapshots', '--json')
Check (@($l.Json.data.snapshots).Count -ge 4) 'the repository holds the snapshots'

Step 'delete files, restore, compare hashes'
Remove-Item -LiteralPath $sessionFile
Remove-Item -LiteralPath $notesFile
Remove-Item -LiteralPath $binFile
Check (-not (Test-Path $sessionFile)) 'the session file is gone'
$ls = Invoke-App $installed @('list', '--sessions', '--json')
$gone = @($ls.Json.data.sessions | Where-Object { $_.id -eq $sessionA })
Check ($gone.Count -eq 1 -and $gone[0].on_disk -eq $false -and $gone[0].in_snapshot -eq $true) 'list shows the session as only in the snapshot'
$rs = Invoke-App $installed @('restore', '--session', $sessionA.Substring(0, 8), '--apply', '--json')
Check ($rs.Code -eq 0 -and $rs.Json.ok -eq $true) 'restore of the session finished'
Check ((Test-Path $sessionFile) -and (Sha $sessionFile) -eq $hashSession) 'the session came back with the same SHA-256'
$rp = Invoke-App $installed @('restore', '--project', "$cyr demo", '--apply', '--json')
Check ($rp.Code -eq 0 -and $rp.Json.ok -eq $true) 'restore of the project finished'
Check ((Test-Path $notesFile) -and (Sha $notesFile) -eq $hashNotes) 'notes.txt came back with the same SHA-256'
Check ((Test-Path $binFile) -and (Sha $binFile) -eq $hashBin) 'the 1 MB file came back with the same SHA-256'
$again = Invoke-App $installed @('restore', '--session', $sessionA.Substring(0, 8), '--apply', '--json')
Check ($again.Code -eq 0 -and (Sha $sessionFile) -eq $hashSession) 'restoring again changes nothing'

Step 'status, doctor'
$st = Invoke-App $installed @('status', '--json')
Check ($st.Code -eq 0 -and $st.Json.ok -eq $true) 'status answers'
$dr = Invoke-App $installed @('doctor', '--json')
Check ($dr.Json -ne $null -and $dr.Json.data.healthy -eq $true) 'doctor finds no problem'

Step 'update'
$u = Invoke-App $installed @('update', '--check', '--json')
$code = ''
if ($u.Json -and $u.Json.error) { $code = $u.Json.error.code }
Check (($u.Json.ok -eq $true) -or $code -eq 'update.no_access' -or $code -eq 'update.no_releases' -or $code -eq 'update.network_failed') 'the check for a new version answers or explains itself'
$upd = Invoke-App $installed @('update', '--from-file', $Exe, '--sha256', (Sha $Exe), '--force', '--json')
Check ($upd.Code -eq 0 -and $upd.Json.data.updated -eq $true -and $upd.Json.data.verified -eq $true) 'update from a file replaced the running program'
Check ((Sha $installed) -eq (Sha $Exe)) 'the installed program is the new file'
Check ((Sha $configFile) -eq $configBefore) 'update left config.json alone'
$v = Invoke-App $installed @('--version', '--json')
Check ($v.Code -eq 0) 'the updated program starts'

Step 'uninstall'
$un = Invoke-App $installed @('uninstall', '--yes', '--json')
Check ($un.Code -eq 0 -and $un.Json.ok -eq $true) 'uninstall finished'
foreach ($name in 'claude', 'projects', 'cleanup', 'check', 'restore-test', 'summary') {
  Check (-not (Test-Task $name)) "the task $name is gone"
}
Check (-not (Test-Path (Join-Path $binDir 'restic-*'))) 'restic is removed'
Check (-not (Test-Path (Join-Path $binDir 'rclone-*'))) 'rclone is removed'
$userPath = (Get-ItemProperty -Path $envKey -Name Path -ErrorAction SilentlyContinue).Path
Check (-not ($userPath -and ($userPath -split ';' | Where-Object { $_.TrimEnd('\') -ieq $binDir }))) 'the program folder left the PATH'
Check (-not (Test-Path (Join-Path $profileDir '.claude\skills\backup'))) 'the skill is removed'
# The running program removes itself a few seconds after it ends.
$deadline = (Get-Date).AddSeconds(30)
$leftovers = Join-Path $binDir 'backupclaude.exe*'
while ((Get-ChildItem -Path $leftovers -ErrorAction SilentlyContinue) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500 }
Check (-not (Get-ChildItem -Path $leftovers -ErrorAction SilentlyContinue)) 'the program file and its leftovers removed themselves'
Check (Test-Path (Join-Path $repoDir 'config')) 'the repository is untouched'
Check ((Get-ChildItem -LiteralPath (Join-Path $repoDir 'data') -Recurse -File | Measure-Object).Count -gt 0) 'the copies are still there'
Check (Test-Path $configFile) 'the settings are kept'
$stored = & cmdkey.exe /list 2>$null | Select-String -SimpleMatch "BackUpClaude$($suffix):repository-password"
Check ($null -ne $stored) 'the password is still in the key store'

Step 'cleanup'
if ($Sandbox) {
  Invoke-Quiet cmdkey.exe @("/delete:BackUpClaude$($suffix):repository-password") | Out-Null
  Invoke-Quiet cmdkey.exe @("/delete:BackUpClaude$($suffix):telegram-bot-token") | Out-Null
  Remove-Item -Path "HKCU:\Software\BackUpClaude$suffix" -Recurse -Force -ErrorAction SilentlyContinue
}
if (-not $KeepWork) { Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue }

Write-Host ''
if ($script:failures.Count -gt 0) {
  Write-Host "FAILED: $($script:failures.Count) checks"
  $script:failures | ForEach-Object { Write-Host "  - $_" }
  exit 1
}
Write-Host 'All checks passed.'
