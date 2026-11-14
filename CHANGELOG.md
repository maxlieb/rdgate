# Changelog

## 0.1.20

- Publishing on Tailscale no longer waits forever. After 90 seconds it stops and says that Funnel must be enabled for the tailnet in the admin console.

## 0.1.19

- Fixed "Only signed in user is allowed to call APIs" when connecting the gateway to sign-in. The sign-in app is registered with a token that has the write:user scope, minted just for that step.

## 0.1.18

- Fixed "403 Forbidden ... required write:user" when connecting the gateway to sign-in. The sign-in app is now registered with the account, and the status token stays read-only.

## 0.1.17

- Setup removes the access tokens that earlier setup runs created, so they do not pile up in the sign-in account.
- Running setup again sets the admin password it shows, so the two agree.
- Installer compression is faster.

## 0.1.16

- Fixed "access token name has been used already" when setup runs again after an earlier attempt. Each run now uses its own token name.

## 0.1.15

- The app asks for administrator rights when it starts. Registering the sign-in service was refused with "Access is denied" because the app ran without them.

## 0.1.14

- Fixed "The task XML contains a value which is incorrectly formatted or out of range" when the sign-in service is registered. The task's logon type was not valid for the schema in use.

## 0.1.12

- Fixed "The task XML is malformed" when the sign-in service is registered. The task definition now follows Windows' element order and declares the encoding it is saved in.

## 0.1.11

- Git is included in the installer, so account setup no longer fails with "git not found" on a PC without Git.

## 0.1.10

- When Tailscale is installed but this PC is not connected to it, the wizard now explains what to do: open Tailscale, sign in, and connect this PC to your tailnet. It continues on its own once connected. The sign-in button was removed.

## 0.1.9

- Tailscale is installed from winget's own source. The Microsoft Store source failed with a certificate error on some networks, and the wizard's command now names the source.
- "Sign in to Tailscale" reads the sign-in address that Tailscale prints, opens it in your browser, and shows it on screen in case the browser does not open.
- If Tailscale's sign-in command fails, the error is shown instead of the step appearing to do nothing.

## 0.1.8

- Tailscale is no longer installed from inside the app. The wizard opens Tailscale's download page and shows the winget command to copy, then you press Check again.

## 0.1.7


## 0.1.6

- Fixed the "Sign in to Tailscale" button doing nothing. The sign-in was stopped as soon as it started.
- Errors from the Tailscale buttons are shown on screen instead of being hidden.

## 0.1.5

- Fixed account setup failing with "no such table: user". The sign-in database is now created before the admin account.

## 0.1.4

- Error messages from the gateway's account setup now show the real cause instead of an unrelated warning.

## 0.1.3

- The gateway program (rdpgw) is now included in the installer. There is no longer a manual build step.
- rdpgw's Apache-2.0 license ships alongside it.

## 0.1.2

- Stopped a console window from flashing on screen each time the app runs a Windows tool such as Tailscale, netsh or schtasks.

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
