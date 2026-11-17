# Sheepdog server

The Sheepdog server finds the open pull requests (PRs) in one GitHub repository where a review is requested from you or from one of your teams. It also runs AI reviews of PRs and gives PRs a danger score from 1 to 10 with [OpenRouter](https://openrouter.ai). The Mac app in `../mac` is a client of this server.

The server serves several users. Each user signs in with an own GitHub token and has an own OpenRouter key, model, repository, reviews, and classifications. The server checks GitHub every 30 seconds for each user who has selected a repository, also when no client is connected. The server keeps its data in one SQLite file. It encrypts the GitHub tokens and the OpenRouter keys in that file.

## Create a GitHub token

Each user signs in with a classic personal access token (PAT). Fine-grained tokens do not work.

1. Open [the new token page](https://github.com/settings/tokens/new?scopes=repo,read:org&description=Sheepdog%20Token). The link selects the scopes `repo` and `read:org`. The server rejects a token without these two scopes.
2. Set an expiration date. A token without an expiration date stays valid until you delete it.
3. Click "Generate token" and copy the token.
4. If your organization uses SAML single sign-on (SSO), open [the token list](https://github.com/settings/tokens), click "Configure SSO" next to the token, and click "Authorize" for the organization. Without this step, GitHub does not show the data of that organization. The server then shows an error with the authorization link from GitHub.

The app sends the token to the server once. To stop the access of the server, delete the token on GitHub.

## Sessions

The server knows a user by the numeric GitHub user ID, not by the login. If a user changes the login, the data stays with the user at the next sign-in. If another person later takes the old login, that person gets a new, empty user.

A sign-in gives the client a session token. Each device has its own session, and a new sign-in does not end the sessions on other devices. A session ends after 30 days without a request. The server deletes ended sessions at start and every hour.

If you lose a device, delete the GitHub token on GitHub, sign in with a new token, and use `DELETE /api/sessions` to end the sessions on all devices.

If you remove a login from `SHEEPDOG_ALLOWED_LOGINS` and restart the server, the server stops polling for that user and rejects its sessions with status 403.

## Configure

The server reads its configuration from environment variables.

| Variable | Required | Default | Value |
| --- | --- | --- | --- |
| `SHEEPDOG_SECRET_KEY` | yes | | Base64 text of 32 random bytes. The server uses it to encrypt tokens and keys. |
| `SHEEPDOG_ALLOWED_LOGINS` | yes | | The GitHub logins that can sign in, separated by commas, for example `alice,bob`. Upper and lower case are the same. |
| `SHEEPDOG_ADDR` | no | `127.0.0.1:8080` | The address and port where the server listens. |
| `SHEEPDOG_DB` | no | `sheepdog.db` | The path of the SQLite file. The server creates it. |

Make the secret key:

```sh
openssl rand -base64 32
```

Keep the secret key in a different place from the backups of the database file. A person who has both can read all tokens and keys. If you lose or change the key, the server cannot read the stored tokens and keys.

If a required variable is missing or wrong, the server stops at once with a message that names the variable.

## Build and run

You need Go 1.27.1 or later. Run all commands in this directory.

```sh
go vet ./... && go test ./...
go build ./cmd/sheepdog
SHEEPDOG_SECRET_KEY='<key>' SHEEPDOG_ALLOWED_LOGINS=alice,bob ./sheepdog
```

The server has no C dependencies. To build for a Linux server on a Mac:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/sheepdog
```

The server stops on SIGINT or SIGTERM. It cancels the running reviews and classifications. Finished ones stay in the database. A request that waits for GitHub or OpenRouter at that time gets status 503 with the text `The server is shutting down`.

## TLS

The server itself does not use TLS. GitHub tokens and session tokens go over the network, so never make the plain HTTP port reachable from other computers. Use one of these setups:

- Keep the default address `127.0.0.1:8080`, and put a reverse proxy with TLS in front of the server.
- Keep the default address, and run `tailscale serve --bg 8080` on the same computer. [Tailscale](https://tailscale.com) then serves the server at `https://<computer>.<tailnet>.ts.net` with a certificate, only to your tailnet. HTTPS certificates must be on in the DNS settings of the tailnet.
- Listen only on a Tailscale IP address, for example `SHEEPDOG_ADDR=100.101.102.103:8080`. Tailscale encrypts the traffic between your devices. Clients then use the IP address, for example `http://100.101.102.103:8080`.

This [Caddy](https://caddyserver.com) configuration gets a certificate for the domain and sends all requests to the server. `flush_interval -1` sends live events to the clients at once.

```caddyfile
sheepdog.example.com {
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

The Mac app accepts plain `http://` only for `localhost`, `127.0.0.1`, `::1`, and other IP addresses, because macOS blocks plain HTTP to host names. For a host name, for example a Tailscale name, the server address must start with `https://`.

After 10 failed sign-ins in a minute from one client IP address, the server rejects sign-ins from that address with status 429 for the rest of the minute. Successful sign-ins do not count. If the connection comes from a loopback address, for example from a reverse proxy on the same computer, the server uses the last address in the `X-Forwarded-For` header as the client address. Caddy sets this header by default. The server ignores the header on other connections, for example on a Tailscale address.

## API

All requests and responses use JSON. An error response has the form `{"error": "text"}`. Each request except `POST /api/session` needs the header `Authorization: Bearer <sessionToken>`. A missing, unknown, or ended session gets status 401. A login that is no longer in `SHEEPDOG_ALLOWED_LOGINS` gets status 403.

GitHub repository names ignore case. The server changes them to lower case, so settings, review requests, tasks, events, stored reviews, and stored classifications always use lower case names.

A review or a classification sends at most 200,000 characters of the diff. The server counts Unicode code points, not bytes and not visible characters. GitHub responses can have at most 50 MB for a diff and 10 MB for other data, and OpenRouter responses at most 10 MB. A larger response gives an error.

| Route | Purpose |
| --- | --- |
| `POST /api/session` | Sign in with `{"githubToken"}`. Returns `{"sessionToken", "login"}`. |
| `DELETE /api/session` | Sign out. Ends this session and closes its event streams. Returns 204. |
| `DELETE /api/sessions` | Sign out on all devices. Ends every session of the user and closes all its event streams. Returns 204. |
| `GET /api/me` | Returns `{"login"}`. |
| `GET /api/settings`, `PUT /api/settings` | `repo`, `model`, and `hasOpenRouterKey`. `PUT` also takes `openRouterKey`. An empty key deletes it. |
| `GET /api/repos` | The repositories of the user. |
| `GET /api/models` | The OpenRouter models that support structured outputs. |
| `GET /api/review-requests` | The result of the last check of the selected repository. |
| `GET /api/repos/{owner}/{name}/pulls/{number}` | The details of a PR. |
| `GET /api/repos/{owner}/{name}/pulls/{number}/review` | The latest stored AI review of the PR, or 404. |
| `POST /api/repos/{owner}/{name}/pulls/{number}/review` | Start an AI review. Returns the task, or 409 if a review of the PR is already running. |
| `GET /api/repos/{owner}/{name}/classifications` | The latest stored classification of each PR of the repository, ordered by PR number. An empty list when there is none. |
| `GET /api/repos/{owner}/{name}/pulls/{number}/classification` | The latest stored classification of the PR, with a `score` from 1 (no danger, can be merged) to 10 (extreme danger) and a `reason`, or 404. |
| `POST /api/repos/{owner}/{name}/pulls/{number}/classification` | Start a classification. Returns the task, or 409 if a classification of the PR is already running. A review and a classification of one PR can run at the same time. |
| `GET /api/tasks` | The running tasks of the user. |
| `DELETE /api/tasks/{id}` | Stop a task. |
| `GET /api/events` | A stream of Server-Sent Events: `requests`, `tasks`, `review`, and `classification`. A new stream of a session ends the older streams of that session. A user can open at most 8 streams at a time. One more stream gets status 429. |

A `requests` or `tasks` event has the full current state. A `review` or `classification` event is sent once when the result is stored or fails, also when the task was stopped after the result was stored. The server never skips these events. If a client reads its stream too slowly, the server ends that stream instead, and the client must connect again. A new stream starts with the current `requests` and `tasks` events, and the client must load the stored reviews and classifications again with `GET .../review` and `GET .../classification`.
