# RD Gateway

Connect to your home Windows PC from anywhere, using the Remote Desktop app you already have. The connection is encrypted over HTTPS, the same way a secure website is. You don't need a VPN, Docker, a server at home, or a Windows domain.

## Who it's for

You want to reach your own PC from outside, and you'd rather not run a homelab to do it.

## What you need

- A Windows 11 Pro PC to reach. The app runs on this PC.
- A free Tailscale account.
- Optional, for a permanent address that looks like any website: a domain name (about $10 a year) and a free Oracle Cloud server, set up once.

## How to connect

Open the address the app gives you in a browser, sign in with your password and an authenticator app, then open the file it gives you in Remote Desktop. Two-factor sign-in is required.

The app offers three ways to reach your PC. You choose one during setup:

- **Tailscale Funnel:** the quickest option. A few clicks, no domain. The address ends in `.ts.net`.
- **Your own address on this PC:** free, if your internet connection allows it. The app checks this for you first.
- **Through a small server of your own:** works on any internet connection. You need a free server and a domain. Setup takes about 20 minutes, once.

## Install

1. Download the installer from the Releases page.
2. Run it as administrator and follow the steps.
3. When the app asks, set up your authenticator app.

The installer is not code-signed yet, so Windows may warn you the first time. Choose **More info**, then **Run anyway**.

## Uninstall

Remove it from Settings, then Apps. The app asks whether to keep your settings and sign-in account.

## What it is not

- **Not a company solution.** For an organization with Active Directory, use Microsoft's Remote Desktop Gateway.
- **Not finished.** This is an early version, still being tested on real machines.

Developer notes, including how it is built and how each piece works, are in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).
