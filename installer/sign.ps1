# Signs the RDGate binaries and installer with the project's self-signed
# code-signing certificate. Used by CI only.
#
# The certificate lives in the repository secrets as SIGNING_PFX_BASE64 and
# SIGNING_PFX_PASSWORD (set once with installer\tools\new-signing-cert.ps1).
# Without them this script does nothing and the build stays unsigned, so
# forks and pull requests still build.
#
#   sign.ps1 -Files build\rdgate.exe, build\rdgwctl.exe -ExportCertificate
#   sign.ps1 -Files dist\*.exe

param(
  [string[]]$Files = @(),
  [switch]$ExportCertificate
)

$ErrorActionPreference = 'Stop'

if (-not $env:SIGNING_PFX_BASE64) {
  Write-Host 'No signing certificate configured: leaving files unsigned.'
  exit 0
}

$tempDir = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
$pfx = Join-Path $tempDir 'rdgate-signing.pfx'
[IO.File]::WriteAllBytes($pfx, [Convert]::FromBase64String($env:SIGNING_PFX_BASE64))

try {
  $flags = [Security.Cryptography.X509Certificates.X509KeyStorageFlags]::EphemeralKeySet
  $cert = [Security.Cryptography.X509Certificates.X509Certificate2]::new($pfx, $env:SIGNING_PFX_PASSWORD, $flags)

  if ($ExportCertificate) {
    # The public half goes into the installer so it can trust itself on first run.
    $cerBytes = $cert.Export([Security.Cryptography.X509Certificates.X509ContentType]::Cert)
    [IO.File]::WriteAllBytes((Join-Path $PSScriptRoot 'rdgate-signing.cer'), $cerBytes)
    Write-Host 'Exported the public certificate into installer\rdgate-signing.cer'
  }

  foreach ($pattern in $Files) {
    foreach ($item in Get-Item $pattern) {
      $result = Set-AuthenticodeSignature -FilePath $item.FullName -Certificate $cert `
        -HashAlgorithm SHA256 -TimestampServer 'http://timestamp.digicert.com'
      # A self-signed certificate is not in the runner's trust store, so the status
      # reads as untrusted even when the signature was written. Check that it was
      # written with our certificate rather than the status text.
      if (-not $result.SignerCertificate -or $result.SignerCertificate.Thumbprint -ne $cert.Thumbprint) {
        throw "Signing $($item.Name) failed: $($result.StatusMessage)"
      }
      Write-Host "Signed $($item.Name)"
    }
  }
}
finally {
  Remove-Item $pfx -Force -ErrorAction SilentlyContinue
}
