<#
.SYNOPSIS
  Installs BackUpClaude for the current user. No administrator rights needed.

.DESCRIPTION
  Downloads the release archive, checks it against the published SHA-256,
  copies the program into %USERPROFILE%\.backupclaude\bin, adds that folder to
  the PATH of the user and starts the setup wizard.

  One command from the release page:
    irm https://github.com/peregoood-ru/BackUpClaude/releases/latest/download/install.ps1 | iex

  With options:
    & ([scriptblock]::Create((irm https://github.com/peregoood-ru/BackUpClaude/releases/latest/download/install.ps1))) -NoSetup

  The program is not signed yet, so Windows SmartScreen may say "Windows
  protected your PC" if you download the file in a browser. This script
  downloads it itself and checks the hash, which SmartScreen does not show.

.PARAMETER Version
  A version such as 0.1.0. The newest published release by default.

.PARAMETER FromFile
  A release archive (.zip) or the program (.exe) on the disk, instead of
  downloading. Use it while the repository is private.

.PARAMETER Sha256
  The expected SHA-256 of -FromFile. Without it a local file is not verified.

.PARAMETER NoSetup
  Install the program only; do not start the wizard.

.PARAMETER NoPath
  Do not change the PATH.

.PARAMETER Repo
  owner/name of the repository with the releases.
#>
param(
  [string]$Version = $env:BACKUPCLAUDE_VERSION,
  [string]$FromFile = '',
  [string]$Sha256 = '',
  [switch]$NoSetup,
  [switch]$NoPath,
  [string]$Repo = 'peregoood-ru/BackUpClaude'
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
try { [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 } catch { }

function Fail([string]$message) {
  Write-Host ''
  Write-Host "BackUpClaude: $message" -ForegroundColor Red
  throw $message
}

# Downloads a release file through the API address, which also works for a
# private repository. The API answers with a redirect to a signed address; the
# token must not travel there, so the redirect is followed by hand.
function Get-Asset([string]$url, [hashtable]$headers, [string]$outFile) {
  $assetHeaders = $headers + @{ Accept = 'application/octet-stream' }
  try {
    Invoke-WebRequest -UseBasicParsing -Uri $url -Headers $assetHeaders -MaximumRedirection 0 -OutFile $outFile
  } catch {
    $response = $_.Exception.Response
    $status = 0
    if ($response) { $status = [int]$response.StatusCode }
    if ($status -ge 300 -and $status -lt 400) {
      $location = [string]$response.Headers['Location']
      if (-not $location) { Fail 'The release server redirected without an address.' }
      Invoke-WebRequest -UseBasicParsing -Uri $location -OutFile $outFile
    } else {
      Fail "Cannot download ${url}: $($_.Exception.Message)"
    }
  }
}

if ($env:OS -ne 'Windows_NT') { Fail 'This installer is for Windows. Download the archive for your system from the releases page.' }

$arch = $env:PROCESSOR_ARCHITECTURE
if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }
if ($arch -ne 'AMD64') { Fail "Windows on $arch is not supported yet: only 64-bit Intel/AMD (amd64)." }

$work = Join-Path ([IO.Path]::GetTempPath()) ('backupclaude-install-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Force $work | Out-Null

try {
  $archive = $null
  if ($FromFile) {
    if (-not (Test-Path -LiteralPath $FromFile)) { Fail "File not found: $FromFile" }
    $archive = (Resolve-Path -LiteralPath $FromFile).Path
    if ($Sha256) {
      $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash
      if ($got -ne $Sha256.ToUpper()) { Fail "The SHA-256 of $archive does not match: expected $($Sha256.ToUpper()), got $got. Nothing was installed." }
      Write-Host 'SHA-256 matches.'
    } else {
      Write-Host 'Note: no -Sha256 given, the local file is not verified.'
    }
  } else {
    $token = $env:BACKUPCLAUDE_GITHUB_TOKEN
    $headers = @{ 'User-Agent' = 'BackUpClaude-installer'; 'X-GitHub-Api-Version' = '2022-11-28' }
    if ($token) { $headers['Authorization'] = "Bearer $token" }

    Write-Host "Looking for the release in $Repo ..."
    try {
      $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=30" -Headers ($headers + @{ Accept = 'application/vnd.github+json' })
    } catch {
      $status = 0
      if ($_.Exception.Response) { $status = [int]$_.Exception.Response.StatusCode }
      if ($status -eq 404 -or $status -eq 401 -or $status -eq 403) {
        Fail ("No access to the releases of $Repo. The repository is private so far, and a private repository shows nothing without a token. " +
          'Set $env:BACKUPCLAUDE_GITHUB_TOKEN to a GitHub token that may read it, or download the zip by hand and run this script with -FromFile <zip> -Sha256 <hash>.')
      }
      Fail "Cannot reach the release server: $($_.Exception.Message)"
    }
    $candidates = @($releases | Where-Object { -not $_.draft -and -not $_.prerelease -and $_.tag_name -match '^v?\d+\.\d+\.\d+$' })
    if ($Version) { $candidates = @($candidates | Where-Object { $_.tag_name.TrimStart('v') -eq $Version.TrimStart('v') }) }
    if ($candidates.Count -eq 0) { Fail 'No published release was found for that version.' }
    $release = $candidates | Sort-Object { [version]($_.tag_name.TrimStart('v')) } -Descending | Select-Object -First 1
    $number = $release.tag_name.TrimStart('v')
    Write-Host "Version $number"

    $zipName = "backupclaude_${number}_windows_amd64.zip"
    $zipAsset = $release.assets | Where-Object { $_.name -eq $zipName } | Select-Object -First 1
    $sumsAsset = $release.assets | Where-Object { $_.name -eq 'checksums.txt' } | Select-Object -First 1
    if (-not $zipAsset) { Fail "The release has no file $zipName." }
    if (-not $sumsAsset) { Fail 'The release has no checksums.txt, so the file cannot be verified.' }

    $archive = Join-Path $work $zipName
    $sumsFile = Join-Path $work 'checksums.txt'
    foreach ($pair in @(@($zipAsset, $archive), @($sumsAsset, $sumsFile))) {
      Write-Host "Downloading $($pair[0].name) ..."
      Get-Asset $pair[0].url $headers $pair[1]
    }
    $line = Get-Content -LiteralPath $sumsFile | Where-Object { $_ -match [regex]::Escape($zipName) } | Select-Object -First 1
    if (-not $line) { Fail "checksums.txt has no line for $zipName. Nothing was installed." }
    $want = ($line -split '\s+')[0].ToUpper()
    $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash
    if ($got -ne $want) { Fail "The downloaded file does not match the published SHA-256 (expected $want, got $got). Nothing was installed." }
    Write-Host 'SHA-256 matches the published checksum.'
  }

  if ($archive -like '*.zip') {
    $unpacked = Join-Path $work 'unpacked'
    Expand-Archive -LiteralPath $archive -DestinationPath $unpacked -Force
    $program = Get-ChildItem -LiteralPath $unpacked -Recurse -Filter 'backupclaude.exe' | Select-Object -First 1
    if (-not $program) { Fail 'There is no backupclaude.exe in the archive.' }
    $exe = $program.FullName
  } else {
    $exe = $archive
  }

  # The program copies itself into its folder and adds that folder to the PATH.
  $installArgs = @('install')
  if ($NoPath) { $installArgs += '--no-path' }
  & $exe @installArgs
  if ($LASTEXITCODE -ne 0) { Fail "The installation step failed (exit code $LASTEXITCODE)." }

  $installed = Join-Path $env:USERPROFILE '.backupclaude\bin\backupclaude.exe'
  if (-not (Test-Path -LiteralPath $installed)) { Fail "The program is not at $installed after the installation." }

  if ($NoSetup) {
    Write-Host ''
    Write-Host "Done. Run:  $installed setup"
  } else {
    Write-Host ''
    & $installed setup
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
  }
} finally {
  Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
