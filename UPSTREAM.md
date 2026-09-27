# Verified upstream contracts

Reviewed 2026-09-27 against CLIProxyAPI v8.0.2 and source commit
`4a2c81864f31f39308e946c4c65e72147855da6e` in
[router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI).
The Dockerfile pins the multi-platform digest
`sha256:b8306b3965755908e1dfcfe0fa114d8d3d4a3df2769ef5e5e3c62cfdf56ee315`.
Its registry manifest includes Linux amd64 and arm64; local runtime checks use
amd64. This is a compatibility snapshot, not a promise about future `latest`.

## Runtime and configuration

The [upstream Dockerfile](https://github.com/router-for-me/CLIProxyAPI/blob/4a2c81864f31f39308e946c4c65e72147855da6e/Dockerfile)
installs `/CLIProxyAPI/CLIProxyAPI` on Debian Bookworm. The wrapper uses `--config`
and the local management `--password` override. It does not set
`MANAGEMENT_PASSWORD`, which can enable remote management.

The [configuration example](https://github.com/router-for-me/CLIProxyAPI/blob/4a2c81864f31f39308e946c4c65e72147855da6e/config.example.yaml)
now uses version 8 sections: `server`, `oauth`, `access`, `management`,
`observability`, and `plugins`. Legacy fields remain accepted, with v8 fields
taking precedence. New files use the v8 layout; old files receive only documented
path/empty-secret migrations. The configured management secret must be nonempty
even when using the local-password override in the tested runtime.
`management.disable-control-panel` disables HTML, not the Management API.

The child working directory is `/data`, so relative file logs persist there.
Plugins use `/data/plugins` but start disabled. Upstream configuration writes are
not atomic; replacing a watched file atomically did not reliably trigger reload
in runtime testing. The wrapper therefore uses YAML node edits, atomic replacement,
and supervised restarts with key verification and rollback for key rotation.

## Management API

Use the current [v8 API documentation](https://help.router-for.me/management/apiv8)
and [management implementation](https://github.com/router-for-me/CLIProxyAPI/tree/4a2c81864f31f39308e946c4c65e72147855da6e/internal/api/handlers/management).
The older `/v0/management` examples in the original request are superseded.
All following paths have prefix `/v8/management`; wrapper requests stay on
`127.0.0.1:8317` with server-only bearer authorization.

| Operation | Method and path | Contract used |
| --- | --- | --- |
| Client keys/readiness | GET `/config/access/api-keys` | JSON string array |
| Account list | GET `/credentials` | `files` array; allowlisted metadata only |
| Account models | GET `/credentials/models?name=...` | `models` array of IDs |
| Disable/enable | PATCH `/credentials/status` | `name`, `auth_index`, `disabled` |
| Disconnect | DELETE `/credentials?name=...&auth_index=...` | Resolve opaque wrapper ID first |
| Start OAuth | GET `/oauth/auth-url?provider=...&is_webui=true` | `url`, `state`; device fields when supplied |
| Poll OAuth | GET `/oauth/status?state=...` | `wait`, `ok`, or `error` |
| Cancel OAuth | DELETE `/oauth/session?state=...` | Cancel upstream session |
| Submit callback | POST `/oauth/callback` | `provider`, `redirect_url` |
| Recent logs | GET `/observability/logs?limit=100` | `lines`; raw content withheld by wrapper |

The upstream callback endpoint is state-bound; the wrapper additionally requires
its authenticated session, CSRF token, and matching opaque flow. It submits the
pasted URL as JSON data and never fetches it.

| Provider | Start provider | Callback provider | Exact callback |
| --- | --- | --- | --- |
| OpenAI / ChatGPT | `codex` | `codex` | `http://localhost:1455/auth/callback` |
| Claude | `claude` | `anthropic` | `http://localhost:54545/callback` |
| Antigravity | `antigravity` | `antigravity` | `http://localhost:51121/oauth-callback` |
| Grok / xAI | `xai` | — | Device-code flow |
| Kimi | `kimi` | — | Device-code flow |

The user's browser localhost is not the Unraid host. Redirect flows support
copying the final localhost URL into the dashboard; device flows use polling.
No callback ports need publishing for these paths. Other providers are omitted
until a supported adapter and tests exist. Account email/plan/model information
is shown only when actually returned; credentials are never downloaded to the UI.

## Unraid and redistribution

The current [submission requirements](https://ca.unraid.net/submit/help),
[repository profile documentation](https://ca.unraid.net/submit/help/repository-info-xml),
and [field reference](https://ca.unraid.net/submit/help/xml-field-reference) describe a
public active repository, root OSI license, nonempty `ca_profile.xml`, valid XML,
and portal Validate/Scan before review. Templates deploy containers; there is no
invented host post-install hook. The template uses bridge networking, two TCP
ports, one appdata mount, and a required masked password with no default.

The upstream [MIT license](https://github.com/router-for-me/CLIProxyAPI/blob/4a2c81864f31f39308e946c4c65e72147855da6e/LICENSE)
permits redistribution with its notice retained. Its exact notice and dependency
notices are under `licenses/` and copied into the image. Upstream source is not
modified. The wrapper and icon have their own root MIT license.

## Validation boundaries

Automated tests cover bootstrap, migration, sessions/CSRF/rate limits, mock OAuth,
callback validation, account operations, key rotation, and supervision. Docker
smoke tests exercise the pinned real runtime on fresh and legacy data. The browser
test uses a separate mock server and intercepts external navigation.

These checks do not establish real provider consent/inference, actual Unraid
Docker UI behavior, arm64 runtime success locally, GHCR publication, or CA
acceptance. Those release checks remain explicit in README.md. The template stays
on the local tag until the activation script verifies a public multiarch image.
