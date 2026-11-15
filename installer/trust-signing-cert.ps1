# Installed beside RDGate. Adds or removes the RDGate signing certificate in the
# machine's Root and Trusted Publishers stores, so updates signed with the same
# key install without publisher warnings. Only this certificate's thumbprint is
# ever added or removed.
param(
  [Parameter(Mandatory)][ValidateSet('Install', 'Remove')][string]$Action,
  [Parameter(Mandatory)][string]$CertPath
)

$ErrorActionPreference = 'Stop'
if (-not (Test-Path $CertPath)) { exit 0 }

$cert = [Security.Cryptography.X509Certificates.X509Certificate2]::new($CertPath)
foreach ($storeName in 'Root', 'TrustedPublisher') {
  $store = [Security.Cryptography.X509Certificates.X509Store]::new($storeName, 'LocalMachine')
  $store.Open('ReadWrite')
  try {
    $present = $store.Certificates | Where-Object { $_.Thumbprint -eq $cert.Thumbprint }
    if ($Action -eq 'Install' -and -not $present) { $store.Add($cert) }
    if ($Action -eq 'Remove' -and $present) { $present | ForEach-Object { $store.Remove($_) } }
  }
  finally {
    $store.Close()
  }
}
