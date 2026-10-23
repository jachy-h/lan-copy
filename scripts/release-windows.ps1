# Run on Windows with Go and Windows SDK signtool.exe on PATH.
# The certificate/private key stays in the current user's certificate store
# (including certificates backed by a hardware token).
[CmdletBinding()]
param(
    [string]$CertificateThumbprint = $env:LAN_COPY_SIGN_CERT_SHA1,
    [string]$TimestampUrl = 'http://timestamp.digicert.com',
    [string]$SignTool = 'signtool.exe',
    [ValidateSet('amd64', 'arm64')]
    [string[]]$Architectures = @('amd64'),
    [string]$Version = 'dev',
    [string]$UpdatePublicKey = $env:LAN_COPY_UPDATE_PUBLIC_KEY,
    [string]$OutputDirectory = 'release/windows-signed'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command failed with exit code $LASTEXITCODE"
    }
}

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw 'Signed Windows releases must be built on Windows.'
}
$CertificateThumbprint = ($CertificateThumbprint -replace '\s', '')
if ($CertificateThumbprint -notmatch '^[0-9a-fA-F]{40}$') {
    throw 'Set LAN_COPY_SIGN_CERT_SHA1 to the SHA-1 thumbprint of your trusted code-signing certificate in Cert:\CurrentUser\My.'
}
$certificate = Get-Item "Cert:\CurrentUser\My\$CertificateThumbprint"
if (-not $certificate.HasPrivateKey -or $certificate.NotAfter -le (Get-Date) -or $certificate.NotBefore -gt (Get-Date)) {
    throw 'The signing certificate must be currently valid and have an accessible private key.'
}
$signToolPath = (Get-Command $SignTool -ErrorAction Stop).Source
$goPath = (Get-Command go -ErrorAction Stop).Source
$repository = Split-Path $PSScriptRoot -Parent
$savedEnvironment = @{}
foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED')) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
$stage = $null
Push-Location $repository
try {
    $output = [IO.Path]::GetFullPath($OutputDirectory)
    New-Item -ItemType Directory -Force -Path $output | Out-Null
    # Work in a unique directory: failed signing never produces a release archive.
    $stage = Join-Path $output ('.stage-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $stage | Out-Null
    # Test on the Windows host before switching to cross-compilation.
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    $env:CGO_ENABLED = '0'
    Invoke-Checked $goPath @('vet', './...')
    Invoke-Checked $goPath @('test', './...')
    $manifestTool = Join-Path $stage 'update-manifest.exe'
    Invoke-Checked $goPath @('build', '-o', $manifestTool, './scripts/update-manifest')
    $env:GOOS = 'windows'
    $archiveNames = @()
    foreach ($architecture in ($Architectures | Select-Object -Unique)) {
        $env:GOARCH = $architecture
        $name = "局域网快传-Windows-$architecture"
        $package = Join-Path $stage $name
        New-Item -ItemType Directory -Path $package | Out-Null
        $exe = Join-Path $package 'lan-copy.exe'
        Invoke-Checked $goPath @('build', '-trimpath', '-ldflags', "-s -w -X main.version=$Version -X main.updatePublicKey=$UpdatePublicKey", '-o', $exe, '.')
        Invoke-Checked $signToolPath @('sign', '/sha1', $CertificateThumbprint,
            '/fd', 'SHA256', '/tr', $TimestampUrl, '/td', 'SHA256',
            '/d', 'Lan Copy', $exe)
        Invoke-Checked $signToolPath @('verify', '/pa', '/all', '/v', $exe)
        $signature = Get-AuthenticodeSignature -LiteralPath $exe
        if ($signature.Status -ne 'Valid' -or $null -eq $signature.TimeStamperCertificate -or
            $signature.SignerCertificate.Thumbprint -ne $CertificateThumbprint) {
            throw "Signature, publisher or timestamp verification failed for $exe"
        }
        $manifestArgs = @('-dir', $package, '-version', $Version, '-os', 'windows', '-arch', $architecture)
        if ($UpdatePublicKey) { $manifestArgs += @('-public-key', $UpdatePublicKey) }
        Invoke-Checked $manifestTool $manifestArgs
        Copy-Item -LiteralPath (Join-Path $repository 'README.md') -Destination $package
        Copy-Item -LiteralPath (Join-Path $repository 'docs/WINDOWS-SECURITY.md') -Destination $package
        Copy-Item -LiteralPath (Join-Path $repository 'release-assets/offline-start.html') -Destination (Join-Path $package '使用说明.html')
        $archiveName = "Lan-Copy-Windows-$architecture.zip"
        Compress-Archive -Path $package -DestinationPath (Join-Path $stage $archiveName)
        $archiveNames += $archiveName
    }
    # Hash the final signed archives, never the unsigned intermediates.
    $checksums = foreach ($archiveName in $archiveNames) {
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $stage $archiveName)).Hash.ToLowerInvariant()
        "$hash  $archiveName"
    }
    $checksums | Set-Content -Encoding ascii -LiteralPath (Join-Path $stage 'SHA256SUMS')
    foreach ($name in ($archiveNames + @('SHA256SUMS'))) {
        Move-Item -Force -LiteralPath (Join-Path $stage $name) -Destination (Join-Path $output $name)
    }
    Write-Host "Signed packages and SHA256SUMS: $output"
} finally {
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
    }
    if ($stage -and (Test-Path -LiteralPath $stage)) {
        Remove-Item -Recurse -Force -LiteralPath $stage
    }
    Pop-Location
}
