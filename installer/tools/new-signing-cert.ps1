# Run once, on your own Windows PC, to create the RDGate code-signing certificate.
#
#   powershell -ExecutionPolicy Bypass -File installer\tools\new-signing-cert.ps1
#
# It writes rdgate-signing.pfx to %USERPROFILE%\RDGate-signing (outside the
# repository), prints a password, and uploads both to the repository secrets
# with the GitHub CLI when it is installed and logged in. Keep the .pfx and the
# password somewhere safe: anyone holding them can sign code that the machines
# which installed RDGate will trust. Do not run this twice unless you mean to
# replace the certificate; every machine would then need a fresh install.

param([string]$OutDir = (Join-Path $env:USERPROFILE 'RDGate-signing'))

$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force $OutDir | Out-Null

$chars = (48..57) + (65..90) + (97..122)
$password = -join ($chars | Get-Random -Count 40 | ForEach-Object { [char]$_ })

$cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject 'CN=RDGate, O=Max Lieberman' `
  -KeyAlgorithm RSA -KeyLength 3072 -HashAlgorithm SHA256 -KeyExportPolicy Exportable `
  -NotAfter (Get-Date).AddYears(10) -CertStoreLocation 'Cert:\CurrentUser\My'

$pfxPath = Join-Path $OutDir 'rdgate-signing.pfx'
Export-PfxCertificate -Cert $cert -FilePath $pfxPath `
  -Password (ConvertTo-SecureString $password -AsPlainText -Force) | Out-Null
Remove-Item "Cert:\CurrentUser\My\$($cert.Thumbprint)"

$b64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes($pfxPath))
$gh = Get-Command gh -ErrorAction SilentlyContinue
if ($gh) {
  $b64 | & gh secret set SIGNING_PFX_BASE64 --repo maxlieb/rdgate
  $password | & gh secret set SIGNING_PFX_PASSWORD --repo maxlieb/rdgate
  Write-Host 'Uploaded SIGNING_PFX_BASE64 and SIGNING_PFX_PASSWORD to maxlieb/rdgate.'
}
else {
  Write-Host 'GitHub CLI not found. Add two repository secrets by hand:'
  Write-Host "  SIGNING_PFX_BASE64 = the contents of $OutDir\rdgate-signing.pfx, base64-encoded"
  Write-Host '  SIGNING_PFX_PASSWORD = the password below'
  Write-Host "Password: $password"
}
Write-Host "Certificate file: $pfxPath"
Write-Host "Save the password in a password manager as well."
