<#
.SYNOPSIS
Deploy Atlas from Windows to a Linux VPS over SSH.
.EXAMPLE
.\scripts\deploy.ps1 -Server root@203.0.113.10 -Bootstrap
.EXAMPLE
.\scripts\deploy.ps1 -Server root@203.0.113.10
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^[a-zA-Z0-9_][a-zA-Z0-9_.-]*@[a-zA-Z0-9][a-zA-Z0-9.-]*$')]
    [string]$Server,

    [ValidateRange(1, 65535)]
    [int]$Port = 22,

    [ValidatePattern('^[a-zA-Z0-9][a-zA-Z0-9_-]*$')]
    [string]$Directory = 'atlas',

    [string]$IdentityFile,

    [switch]$Bootstrap
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$projectDirectory = Split-Path -Parent $PSScriptRoot

foreach ($command in @('tar', 'ssh', 'scp')) {
    if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "Required command is missing: $command"
    }
}

$files = @(
    'docker-compose.yml', '.env.example', 'README.md', 'scripts'
)
if ($Bootstrap) {
    # Secrets are included only on the explicitly requested first deployment.
    $files += @('.env', 'secrets/google-credentials.json', 'data/google-token.json')
}
foreach ($file in $files) {
    if (-not (Test-Path -LiteralPath (Join-Path $projectDirectory $file))) {
        throw "Missing local file: $file. Configure .env and complete Google OAuth before -Bootstrap."
    }
}

$sshArguments = @('-p', "$Port")
$scpArguments = @('-P', "$Port")
if ($IdentityFile) {
    $identityPath = (Resolve-Path -LiteralPath $IdentityFile).Path
    $sshArguments += @('-i', $identityPath)
    $scpArguments += @('-i', $identityPath)
}

$bundleName = 'atlas-deploy-' + [Guid]::NewGuid().ToString('N') + '.tar.gz'
$bundlePath = Join-Path ([System.IO.Path]::GetTempPath()) $bundleName
$bootstrapArgument = if ($Bootstrap) { '1' } else { '0' }

try {
    Write-Host 'Packaging Atlas...'
    & tar -czf $bundlePath -C $projectDirectory @files
    if ($LASTEXITCODE -ne 0) { throw 'Packaging failed.' }

    Write-Host "Uploading to $Server..."
    & scp @scpArguments $bundlePath "${Server}:$bundleName"
    if ($LASTEXITCODE -ne 0) { throw 'Upload failed.' }

    Write-Host "Deploying in ~/$Directory..."
    $remoteScript = [System.IO.File]::ReadAllText((Join-Path $PSScriptRoot 'deploy-remote.sh')).Replace("`r`n", "`n")
    # All arguments interpolated into the remote command are validated or generated.
    $remoteScript | & ssh @sshArguments -T $Server "bash -s -- $Directory $bundleName $bootstrapArgument"
    if ($LASTEXITCODE -ne 0) { throw 'Remote deployment failed. Check the output above.' }

    Write-Host 'Deployment commands completed. Test /start and a task in Telegram.'
}
finally {
    if (Test-Path -LiteralPath $bundlePath) {
        Remove-Item -LiteralPath $bundlePath -Force
    }
}
