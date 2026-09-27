# CLIProxyAPI for Unraid

A small Go supervisor and browser dashboard around the upstream CLIProxyAPI
runtime. It creates configuration and keys automatically, persists your accounts,
and connects providers through the Management API. No Compose, SSH, YAML editing,
or terminal OAuth login is needed for normal installation.

CLIProxyAPI is a third-party project. Provider availability and authentication
methods are controlled by their respective services. This is not an official
OpenAI or Unraid application.

**Release status:** the template uses the public
`ghcr.io/ozoneh3/cliproxyapi-unraid` image. No Community Applications submission
was made during this work. Local development commands use
`cliproxyapi-unraid:local`.

## Install on Unraid

After the image is public and the CA listing has been accepted:

1. **Apps → CLIProxyAPI → Install**.
2. Choose a unique **WebUI Password**, between 12 and 72 bytes. Keep the default
   ports and appdata path unless they conflict with another application.
3. Click **Apply**, wait for the container to start, then click **WebUI**.
4. Sign in with that password. Open **Providers → OpenAI / ChatGPT → Connect**.
5. Sign in and approve access on the provider page. If the browser ends on an
   unreachable `http://localhost:1455/auth/callback?...` page, copy its complete
   address into the dashboard's callback field. The dashboard completes the
   exchange without an SSH tunnel. Keep the callback URL private.
6. Once **OpenAI connected** appears, open **API Access** and copy the base URL
   and client API key into your OpenAI-compatible client. Select a model listed
   on Overview.

The default URLs are:

| Purpose | URL |
| --- | --- |
| Dashboard | `http://UNRAID-IP:8318/` |
| OpenAI-compatible API | `http://UNRAID-IP:8317/v1` |

If a popup is blocked, use the provider sign-in link shown in the dashboard.
Device-code providers show a verification link and a code when supplied by
upstream. **Cancel login** releases a pending flow so you can try again.

Use a trusted LAN or VPN. Do not publish ports 8317 or 8318 directly to the
internet. HTTPS deployments should terminate TLS at a trusted reverse proxy,
retain the original Host/Origin relationship, and enable **HTTPS-only Session
Cookie**. The API address displayed by default remains HTTP on the API host port;
configure clients with your separate HTTPS API URL when using a reverse proxy.

For local template testing before CA publication, build the image on the Unraid
host (or load an image archive), copy `templates/cliproxyapi.xml` to
`/boot/config/plugins/dockerMan/templates-user/my-CLIProxyAPI.xml`, and select
**Docker → Add Container → CLIProxyAPI**. Keep the name `CLIProxyAPI`. This local
maintainer workflow is not a claim that the app is already in CA.

## Settings and persistence

| Template entry | Default | Meaning |
| --- | --- | --- |
| API Port | `8317:8317/tcp` | Proxy API |
| WebUI Port | `8318:8318/tcp` | Authenticated dashboard |
| Appdata | `/mnt/user/appdata/cliproxyapi:/data` | One persistent directory |
| WebUI Password | none, required, masked | Dashboard login |
| API Public Port | `8317` | Advanced: match a changed API host port |
| HTTPS-only Session Cookie | `false` | Advanced: enable only with HTTPS |

Bridge networking, `sh`, unprivileged execution, and
`--restart=unless-stopped` are configured. Unraid's Docker-tab **Autostart**
setting separately controls array-start behavior. There is no Docker socket,
host network, device mount, or added capability. OAuth callback ports are not
published: the wrapper submits callbacks through the local Management API, and
device-code flows do not need incoming callbacks.

```text
/data/
├── config.yaml
├── auths/
├── logs/
├── plugins/
└── state/
    ├── password.hash
    ├── local-management.key
    └── config.before-*.yaml       # only after migration or rotation
```

A private `.wrapper.lock` prevents concurrent wrappers sharing the same appdata.
Directories use mode 0700 and wrapper-owned files use mode 0600. The child inherits
umask 0077. The image follows upstream's root runtime; host appdata ownership may
therefore be root. Do not make appdata publicly accessible through SMB or HTTP.
Backups contain secrets and need the same protection as live data.

New configuration uses the current v8 layout, including `server.port: 8317`,
`oauth.auth-dir: /data/auths`, `access.api-keys`,
`management.allow-remote: false`, and `management.disable-control-panel: true`.
The generated management key is bcrypt-hashed in YAML; its separate plaintext
local transport secret stays server-side in `state/local-management.key`.
The client key contains 32 random bytes, encoded as `sk-cpa-` plus 64 hex digits.
Plugins remain disabled initially; their directory is `/data/plugins`.
The child works in `/data`, so upstream's relative `logs` directory is persistent.

Changing **WebUI Password** in the template and clicking Apply replaces the
stored bcrypt hash on the next startup. An unchanged value does not rewrite it.
Sessions expire after eight hours and are invalidated on restart/password change.
The wrapper never writes this plaintext password to appdata and removes it from
its environment before launching the child. Unraid/Docker still stores the
configured environment value in its own template/container metadata; Mask hides
it in the form, not from the server administrator.

## Dashboard features

- Overview: process/API status, reported upstream version, copyable endpoint,
  client key, available models, and connected accounts.
- Providers: Codex, Claude, Antigravity, Grok/xAI, and Kimi adapters; reconnect,
  cancellation, enable/disable, confirmed disconnection, and per-account models.
- API Access: copy the client key and regenerate it with explicit confirmation.
- Logs: recent event severities. Arbitrary log text is withheld because it can
  contain tokens, authorization codes, or authorization headers.

Account email and plan are shown only if the Management API supplies them.
No raw credential JSON, OAuth tokens, management secret, arbitrary API proxy,
filesystem browser, command runner, or plugin installer is exposed by this UI.
The optional advanced/raw YAML editor is deliberately not included.

Key rotation backs up the file, adds a new key while retaining the old one,
verifies the new key against `/v1/models`, then retires the previous primary key
and verifies its rejection. Additional existing client keys are preserved.
Atomic replacements are followed by supervised child restarts because the
upstream file watcher did not reliably reload replaced inodes during testing.
Expect a brief API interruption; finish or cancel pending OAuth flows first.
A failed verification triggers restoration of the backup configuration.

## Existing installations and migration

Stop the old container and back up its appdata before switching images. Keep
`/mnt/user/appdata/cliproxyapi` as the host path, replace the individual mounts
with the single `/data` mount, and choose a WebUI password. Do not run both images
against the same files simultaneously; the wrapper lock cannot lock an old
upstream-only container.

The wrapper reuses `config.yaml`, `auths`, `logs`, and `plugins`. It preserves
non-empty API keys, non-empty management keys, provider credentials, and unknown
YAML fields. Both legacy and v8 configuration layouts are accepted, with v8
values taking precedence just as upstream specifies.

Necessary migration changes are limited to:

- `/root/.cli-proxy-api` or `~/.cli-proxy-api` → `/data/auths`.
- An explicit `/CLIProxyAPI/plugins` → `/data/plugins`.
- If the management secret is absent or empty, initialize that previously unset
  field with a generated bcrypt hash. This is needed because the tested upstream
  middleware checks for a configured secret before accepting its local-password
  override. An existing non-empty secret is never replaced. Existing remote
  management policy is preserved; review any previously enabled remote access.

Before these changes, the exact original bytes are saved once as
`state/config.before-wrapper-migration.yaml`. YAML node editing preserves unknown
fields and comments; indentation and quoting can normalize. Files needing no
migration are not rewritten by the wrapper. Upstream itself may hash a preexisting
plaintext management key when loading the configuration.

An existing configuration with no client keys, a different internal API port,
TLS enabled directly on the child, or a non-loopback-reachable host is rejected
rather than silently resetting settings. This wrapper targets the original
8317/HTTP deployment. Custom external auth directories are not automatically
moved; review advanced custom paths before migrating. No auth data is deleted.

## Architecture

```text
Browser ── :8318 ── Go wrapper: sessions / CSRF / narrow API / embedded UI
                         │
                         └── 127.0.0.1:8317/v8/management
                                      │
API clients ── :8317/v1 ── upstream CLIProxyAPI child
                                      │
                                  /data appdata
```

The Go wrapper is PID 1. It initializes storage, launches
`/CLIProxyAPI/CLIProxyAPI --config /data/config.yaml --password <local-secret>`,
forwards SIGTERM/SIGINT/SIGHUP, reaps children, and fails the container if the main
child exits unexpectedly. Trusted internal restart requests are used for atomic
key updates. Shutdown closes the WebUI gracefully and escalates to SIGKILL if the
child does not exit within ten seconds. Upstream output is redacted before it is
written to Docker logs, so safe startup failures remain diagnosable without
logging OAuth URLs or credentials.

Current upstream versions enable a ten-second local-management watchdog when the
wrapper supplies its local `--password` override. The wrapper calls the fixed
loopback `/keep-alive` endpoint every three seconds; it does not expose this
endpoint or password outside the container.
The local management argument can be inspected by the Docker/host administrator,
who already controls the container and appdata; it is not a tenant isolation
boundary.

`/healthz` requires a live child and an authenticated response from its models
endpoint. `/readyz` additionally checks the Management API. Neither requires a
provider account. The Docker healthcheck probes `/healthz` through the wrapper
itself. These unauthenticated endpoints return only coarse status.

## Local development and tests

Requirements: Go 1.26+, Python 3, Node.js for frontend syntax tests, and Docker.
No provider credentials are needed.

```sh
go mod download
go vet ./...
go test -race ./...
go build ./...
python3 scripts/validate-template.py
node --check web/static/app.js
docker build -t cliproxyapi-unraid:local .
python3 scripts/smoke.py
python3 scripts/smoke.py cliproxyapi-unraid:local --legacy
```

The smoke script creates an isolated volume and random loopback-only host ports,
tests the real upstream runtime, then removes its own container and volume.
It checks configuration creation, login, API auth, localhost-only management,
CSRF, key rotation, restarts, permissions, health, and child-crash failure.
The `--legacy` run also checks preservation of the previous key, old data,
comments, unknown fields, and the exact migration backup. No real OAuth occurs.

For an interactive local container (fish):

```sh
docker rm CLIProxyAPI-test
docker build -t cliproxyapi-unraid:local .
read -sP 'Choose a WebUI password (12-72 bytes): ' WEBUI_PASSWORD
set -lx WEBUI_PASSWORD $WEBUI_PASSWORD
docker volume create cliproxyapi-test
docker run -d --name CLIProxyAPI \
  -p 127.0.0.1:8317:8317 -p 127.0.0.1:8318:8318 \
  -e WEBUI_PASSWORD \
  -v cliproxyapi-test:/data \
  cliproxyapi-unraid:local
set -e WEBUI_PASSWORD
```

Open `http://127.0.0.1:8318/`. Use the UI to connect an account. Stop with
`docker stop -t 15 CLIProxyAPI`; the named volume persists. Remove the test
container with `docker rm CLIProxyAPI` when finished. Do not reuse that name if
another container already has it.

For development without the upstream binary or an OAuth account:

```sh
go run ./cmd/mock-cliproxy
```

Open `http://127.0.0.1:18318/` using the explicitly development-only password
`development-only-password`. This separate command binds both mock services to
loopback and deletes its temporary data on exit. The production image does not
include the mock executable or use this password. For mock Codex consent, paste:

```text
http://localhost:1455/auth/callback?state=mock-state&code=mock-code
```

Do not authenticate at a real provider when using mock mode. Its OAuth URL is a
fixture. Browser testing intercepts external navigation and supplies mock consent:

```sh
npm install --prefix /tmp/cpa-ui playwright@1.58.2 --no-audit --no-fund
NODE_PATH=/tmp/cpa-ui/node_modules node scripts/ui-test.cjs
```

Run that command while the mock server is running. It uses `/usr/bin/chromium`
(or `CHROMIUM_PATH`) and writes a screenshot to `/tmp/cpa-dashboard.png`.
The headless test uses `--no-sandbox` only for the disposable local test browser.
There are no npm or CDN dependencies in the shipped frontend.

## Build and publish

The Dockerfile builds a static Go wrapper and layers it onto the upstream image,
without modifying or rebuilding upstream source. It pins the tested v8.0.2
multi-platform image digest; updating that digest is an intentional maintenance
change. Upstream's manifest contains Linux amd64 and arm64. Local runtime testing
was performed on amd64; CI includes native amd64 and arm64 build/test jobs.

`.github/workflows/build.yml` runs Go tests, race checks, vet, XML and JavaScript
validation, a Docker build, and real-upstream smoke tests. Pull requests never
publish. `.github/workflows/publish.yml` first calls those checks, then publishes
from a version tag such as `v1.0.0`, or a manual run on `main`, using
`GITHUB_TOKEN` with `packages: write`. Tags include semver, commit SHA, and `latest`
for stable releases/manual main builds. Prereleases do not replace stable latest.

The published image name is **`ghcr.io/ozoneh3/cliproxyapi-unraid`**. Registry
repository names must be lowercase; GitHub repository links retain `OzoneH3`.
No registry credentials are committed. Set the GHCR package visibility to public.
After a public multi-platform `latest` image exists:

```sh
python3 scripts/activate-release.py
python3 scripts/validate-template.py
```

The activation script performs an anonymous GHCR manifest check and only then
changes the template from the local build tag to the public image. Review and
commit that change. It does not publish anything or create a CA submission.
Verify the raw template, README, support, and PNG URLs on the public repository.

### Publishing to Unraid Community Applications

Follow the [current submission portal](https://ca.unraid.net/submit/help).
Publish an active public repository with an OSI-approved license and a non-empty
root `ca_profile.xml`. This repository includes both, along with its own icon.
After image activation and actual Unraid validation, open the
[new submission flow](https://ca.unraid.net/submit/new), run **Validate** and
**Scan**, resolve findings, inspect the preview, and submit for review.
Do not submit the development template while its Repository is the local tag.

Before submission, load/save the template through Unraid's Docker UI and compare
its output with dockerMan-generated XML. Test a fresh installation, migration,
host-port changes, WebUI URL substitution, password changes, Unraid Autostart,
container updates, and persistence. Complete real provider OAuth in a browser on
a different computer, including callback paste and device-code consent. These
Unraid/provider checks cannot be replaced by mock tests.

## Advanced troubleshooting and recovery

- **Container exits immediately:** inspect `docker logs CLIProxyAPI` privately.
  Check password length, appdata permissions, port conflicts, and whether an old
  `config.yaml` path is actually a directory. Do not delete real configuration.
- **API URL wrong after changing a host port:** update **API Public Port** in
  advanced template settings to match. The internal port stays 8317.
- **Login repeats behind HTTPS:** enable secure cookies only when using HTTPS,
  and preserve Host/Origin through the proxy. Forwarded headers are not trusted.
- **API model list is empty:** connect a provider and refresh models. A model list
  alone does not prove a real inference request succeeds for that provider.
- **OAuth rejected/expired:** start a fresh flow, consent again, and paste the
  exact callback URL for that flow. Do not edit its host, path, state, or code.
- **Logs page unavailable:** legacy configuration may have file logging disabled.
  Detailed upstream files remain under appdata/logs and can contain sensitive
  material; the browser only displays safe event summaries.
- **Interrupted/failed key rotation:** stop the container, back up current
  appdata, and inspect `state/config.before-key-rotation.yaml` privately. Restore
  it to `config.yaml` if needed, then restart. Never share backups containing keys.
- **Undo wrapper migration:** stop the container and restore the exact
  `state/config.before-wrapper-migration.yaml` backup, then restore the old image
  and its original mounts. Preserve `auths`; never delete it to repair setup.

Do not run concurrent config writers or edit the file during rotation. The wrapper
backs up and atomically replaces its writes, but cannot make external/upstream
writers transactional. Existing unusual YAML aliases or custom paths should be
reviewed before migration. Recheck the pinned upstream image before upgrading.

## Security and licenses

Sessions use 256-bit random identifiers, HttpOnly/SameSite=Strict cookies, bounded
in-memory storage, and fixed expiry. Mutations require a session, matching Origin,
and a separate CSRF token. Login attempts are limited per socket peer; spoofable
forwarded IP headers are ignored. Secure cookies are opt-in for HTTPS because
browsers will not send them over normal Unraid HTTP. HTTP is not encrypted.

Only explicitly selected Management API fields reach the frontend. Flow IDs are
opaque, expiring, session-bound, and provider-bound. Callback URLs are parsed and
checked for exact scheme/host/path/state, duplicate parameters, and replay; they
are sent as data to a fixed loopback endpoint and never fetched. The browser still
receives the provider authorization URL (which necessarily contains OAuth state),
but never a separate upstream state field, management key, or OAuth token.

CSP, frame protection, no-sniff, no-referrer, no-store responses, bounded request
bodies, and text-only DOM rendering provide additional protections. The app does
not trust third-party script/CDN content. The API key is intentionally retrievable
only by authenticated dashboard users. Host/Docker administrators remain trusted.

The wrapper and original icon are MIT-licensed. Upstream CLIProxyAPI is separately
MIT-licensed and permits redistribution subject to retaining its notice.
`licenses/CLIProxyAPI.txt` retains the exact upstream notice; Go dependency notices
are also included and copied into the image. The base image retains its Debian
package notices. This repository does not claim authorship of upstream software.
See [UPSTREAM.md](UPSTREAM.md) for verified contracts, paths, and source references.
