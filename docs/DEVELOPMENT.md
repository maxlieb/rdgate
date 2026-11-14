# RD Gateway Manager

Stands up a self-hosted Remote Desktop Gateway on a Windows PC and publishes it
over Tailscale Funnel, so a remote machine can connect with nothing installed on
it beyond the Remote Desktop client that already ships with Windows.

Two front doors. Everything public is on **443 only**, because a managed
corporate client is frequently permitted outbound 443 and nothing else.

**Funnel** (default, free, no domain, works behind CGNAT):

```
browser / mstsc
  -> <machine>.<tailnet>.ts.net:443    Funnel, TLS terminated by Tailscale
  -> 127.0.0.1:8081                    Caddy, splits by path
       /            -> 127.0.0.1:8080  rdpgw   (gateway)
       /auth        -> 127.0.0.1:3000  Gitea   (OIDC provider, TOTP enforced)
```

**Your own domain** (survives DNS-layer filtering, needs a routable address):

```
browser / mstsc
  -> rdgw.example.com:443              Caddy, own certificate via ACME
       rdgw.example.com -> rdpgw
       auth.example.com -> Gitea
```

The second mode exists because of categorisation, not capability. Filtering
products such as Cisco Umbrella ship categories for dynamic DNS and for
remote-access vendors, so `*.ts.net` and `*.duckdns.org` are classifiable by
name. A personal domain usually is not. If the client is a managed machine,
use your own domain.

Tailscale Funnel **cannot** be fronted by a CNAME from your own domain: its
ingress routes by SNI and only recognises `.ts.net` names, and the certificate
is issued for that name, so `mstsc` would reject the mismatch with no way to
click through. The two modes are alternatives, not layers.

Funnel publishes with `--tls-terminated-tcp` rather than its HTTP proxy,
because RD Gateway speaks non-standard verbs (`RDG_IN_DATA` / `RDG_OUT_DATA`)
that an HTTP-aware proxy has no reason to understand. Caddy reverse-proxies
them without complaint, since it does not care about methods.

## Layout

```
cmd/rdgwctl/         CLI: install, uninstall, status, render
cmd/rdgw-manager/    Wails desktop app (build tag: wails)
internal/provision/  all the logic; no third-party dependencies
internal/ui/         the methods the frontend calls
frontend/            setup wizard and dashboard
installer/           Inno Setup script
```

`internal/provision` deliberately uses only the standard library, so the core
builds and tests anywhere without a module proxy.

## Prerequisites

**rdpgw.exe.** The installer builds it in CI from the pinned `468f70c (main, pinned)` tag and
ships it beside the app. To build it by hand, which is only needed off CI:

```
git clone https://github.com/bolkedebruin/rdpgw
cd rdpgw
GOOS=windows GOARCH=amd64 go build -o rdpgw.exe ./cmd/rdpgw
```

The app copies it to `C:\ProgramData\RDGateway\rdpgw\rdpgw.exe` on install.

**Funnel permission.** Funnel has to be allowed for the node in the tailnet
policy file (`nodeAttrs` with the `funnel` attribute). Without it the publish
step fails with a message pointing at the admin console.

Gitea is downloaded automatically and verified against its published SHA256.
Caddy is downloaded from its official GitHub release and the binary extracted
from the archive.

**Funnel mode runs Gitea under the `/auth` subpath**, which is how both
services fit on one port. Gitea supports this via `ROOT_URL`, but it is the
configuration it is least exercised in; if sign-in misbehaves in Funnel mode
and works in domain mode, that is the first thing to suspect.

## Build

```
go test ./...                 # core, no toolkit needed
go build ./cmd/rdgwctl        # the CLI

go install github.com/wailsapp/wails/v2/cmd/wails@latest
go mod tidy
wails build -tags wails       # the desktop app
```

Then the installer:

```
mkdir build
copy build\bin\rdgw-manager.exe build\
go build -o build\rdgwctl.exe ./cmd/rdgwctl
iscc installer\rdgw-manager.iss          # -> dist\RDGatewayManager-Setup-*.exe
```

The uninstaller runs `rdgwctl uninstall` **before** deleting files, so the
scheduled tasks are removed and Funnel is withdrawn while the binaries still
exist. Deleting the files first would leave a machine published to the
internet with nothing behind it. It asks whether to keep your settings and
sign-in account, and says where they were left if you keep them.

## Use

```
rdgwctl render -host pc.tailnet.ts.net -target win-vm:3389   # writes nothing
rdgwctl install -target win-vm:3389                          # Funnel (default), elevated

rdgwctl check-direct                                         # can direct work here?
rdgwctl install -door direct -target win-vm:3389 \
        -domain myhome.duckdns.org -duckdns myhome:TOKEN
rdgwctl install -door direct -target win-vm:3389 \
        -domain rdgw.example.com -ddns-url https://provider/update?token=...

rdgwctl install -door relay -target win-vm:3389 -domain rdgw.example.com
rdgwctl relay-script          # print the server script again

rdgwctl status
rdgwctl uninstall             # keeps data; add -purge to delete it
```

Uninstall also removes the firewall rules it added, the router's port mapping
(best effort; the lease expires on its own otherwise) and the address-updater
task.

`render` prints every generated config without touching disk. Worth reading
once before trusting the installer with a machine.

## Notes

Two-factor enrollment is **enforced** in Gitea's config, so the portal cannot be
reached until an authenticator is set up. There is no API for TOTP enrollment —
it exists only in Gitea's web UI — so the app opens that page rather than
rendering the QR itself.

The stored Gitea token is scoped `read:admin`. Its only job is answering "has
anyone enrolled", and a token on disk should not be able to change anything.

The admin password is shown once at install and never written to disk.

Services run as scheduled tasks rather than real services: neither binary is
service-aware, so `sc.exe` would start them and then kill them. The task XML
(rather than `schtasks` flags) is what buys a working directory and
restart-on-failure.

### Client side

`RDGHelper.ahk` and its `.ini` go on the *connecting* machine. The installer
writes a matching `RDGHelper.ini` into the install root; copy it next to the
script. With no hostname configured the script disables its `.rdp` rewriting
and auto-Connect rather than matching every file, since an empty needle
matches everything.

### Not yet verified on Windows

Everything is unit-tested on Linux and cross-compiles for Windows, but these
surfaces only exist there and have not been run: `schtasks` accepting the
generated XML, `netsh advfirewall` rule syntax, `gitea admin user
generate-access-token --raw` output, `tailscale funnel --tls-terminated-tcp`
argument form, the Wails build, and UPnP against a real Bezeq router.

### Not yet done

Code signing. An unsigned installer that registers services and opens ports
gets SmartScreen's "Windows protected your PC" as the user's first impression.
Azure Trusted Signing works here (`signtool` integrates with it as a service),
unlike the `.rdp` signing case where rdpgw needs a key file on disk.
