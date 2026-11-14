# Changelog

## 0.1.1

- Fixed the first-run wizard showing an empty window. The desktop app could not find its backend, so no step appeared. Backend errors are now shown on screen.
- Removed test screenshots that were committed by mistake.
- The installer's file name and version now match the release.

## 0.1.0

- First release. Three ways to reach the gateway, chosen in the wizard:
  - Tailscale Funnel: free, no domain needed.
  - Your own address on this PC: free, with automatic router setup over UPnP and a DNS updater.
  - Through a small server of your own: free server and domain, with a generated setup script.
- Two-factor sign-in is required, with the authenticator set up in the browser.
- Uninstall removes the scheduled tasks, firewall rules and router mapping, and asks whether to keep your settings.
- Build and installer run in GitHub Actions on Windows.
