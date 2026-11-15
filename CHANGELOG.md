# Changelog

## 0.1.37

- The app's icon is the RDGate logo. Before, `.gitignore` hid the icon files from git, so the build used Wails' default W.
- The speed check is on the first setup screen, where the three ways in are chosen, so its results can inform that choice. It runs when the screen opens and has a "Measure again" button.

## 0.1.36

- Renamed to RDGate. The window, installer, Start menu entry and executable use the new name. Upgrading removes the old rdgw-manager.exe and the old Start menu folder. Existing install folders keep their location, and the scheduled task names stay as they were, so nothing on a running gateway changes.
- New logo: a gate with a remote screen behind it. It appears in the window header, the taskbar and the installer.
- Settings opens a settings panel showing the way in, the addresses, the sign-in account and the allowed computers. "Open install folder" is inside it.

## 0.1.35

- Setup asks for the sign-in account's name and, optionally, its password. Leave the password empty and one is made and shown once, as before.
- A speed check in setup times this PC's Funnel address and the nearby Oracle Cloud regions, and shows which region answers fastest from here. It changes nothing.
- "Manage users" on the dashboard opens Gitea's user administration on this PC, on the loopback address, so the admin pages stay off the public address.

## 0.1.34

- Sign-in requires two-factor. Caddy sends the two OAuth steps that issue a sign-in code through a small gate first. An account without two-factor turned on gets a page that says so, and no code. Accounts with two-factor, and anyone not yet signed in, go through as before.
- The Two-factor line now reports whether the gate is running and routed. "Repair gateway" installs the gate and its routes on an existing setup.

## 0.1.33

- Dashboard: Stop gateway, Start gateway, and Change password. The new password is shown once, like at setup.
- The Two-factor line no longer says "enrolled". Gitea's API could not filter by two-factor status, so it reported enrollment for every account. It now says that two-factor is not enforced yet.

## 0.1.32

- The sign-in and server pages show their logos. The gateway's assets folder ships with the installer and is copied into place on setup and on Repair.

## 0.1.31

- "Repair gateway" is always on the dashboard footer, not only when the gateway check fails. Use it to copy the web pages into place on an existing install.

## 0.1.30

- The gateway's web pages ship with the installer and are copied into place on setup and on Repair. Without them the sign-in page showed a bare fallback with a disabled server list.

## 0.1.29

- The public-address check recognises the TCP Funnel the app publishes. Before, a working Funnel was reported as "not published".

## 0.1.28

- Publishing no longer hangs forever when Tailscale leaves a helper process running. Commands are cut off a few seconds after their timeout.
- An action that does not answer within two minutes shows an error instead of staying on "Working…".

## 0.1.27

- Fixed the gateway not starting: its sign-in provider URL now matches the issuer Gitea advertises, with the trailing slash. The log showed "issuer did not match".
- Repair gateway also rewrites rdpgw.yaml from the saved settings, so an existing install gets the fix without reinstalling.

## 0.1.26

- Settings opens the install folder. It was missing on the Go side, so the button did nothing.
- The dashboard redraws only when the status changes. The "checked" time is updated on its own, so the list no longer rebuilds every refresh.

## 0.1.25

- Dashboard no longer redraws when nothing changed. Buttons respond to clicks, and text can be selected and copied while it refreshes.

## 0.1.24

- Dashboard: "Repair gateway" rewrites the gateway's scheduled task so it logs to rdpgw\rdpgw.log, then restarts it. Nothing is reinstalled.

## 0.1.23

- The gateway's output is written to rdpgw\rdpgw.log, so a gateway that does not start leaves a log to read.

## 0.1.22

- Publishing before setup no longer fails with "invalid port 0": the default port is used when the config is not saved yet.
- The Funnel step no longer reads the access rules to decide. It reports the result of Publish, and the rules snippet appears only after a failed publish.

## 0.1.21

- Funnel now has its own wizard step. It checks MagicDNS, HTTPS certificates and Funnel permission before Publish, and opens the Tailscale page for whichever one is off.
- When Tailscale refuses Funnel, the app keeps the link it printed and offers to open it.

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
