# Review Queue server

The Review Queue server finds the open pull requests (PRs) in one GitHub repository where a review is requested from you or from one of your teams. It also runs AI reviews of PRs with [OpenRouter](https://openrouter.ai). The Mac app in `../mac` is a client of this server.

The server serves several users. Each user signs in with an own GitHub token and has an own OpenRouter key, model, repository, and reviews. The server checks GitHub every 30 seconds for each user who has selected a repository, also when no client is connected. The server keeps its data in one SQLite file. It encrypts the GitHub tokens and the OpenRouter keys in that file.

## Create a GitHub token

Each user signs in with a classic personal access token (PAT). Fine-grained tokens do not work.

1. Open [the new token page](https://github.com/settings/tokens/new?scopes=repo,read:org&description=Review%20Queue). The link selects the scopes `repo` and `read:org`. The server rejects a token without these two scopes.
2. Set an expiration date. A token without an expiration date stays valid until you delete it.
3. Click "Generate token" and copy the token.
4. If your organization uses SAML single sign-on (SSO), open [the token list](https://github.com/settings/tokens), click "Configure SSO" next to the token, and click "Authorize" for the organization. Without this step, GitHub does not show the data of that organization. The server then shows an error with the authorization link from GitHub.

The app sends the token to the server once. To stop the access of the server, delete the token on GitHub.

## Configure

The server reads its configuration from environment variables.

| Variable | Required | Default | Value |
| --- | --- | --- | --- |
| `REVIEWQ_SECRET_KEY` | yes | | Base64 text of 32 random bytes. The server uses it to encrypt tokens and keys. |
| `REVIEWQ_ALLOWED_LOGINS` | yes | | The GitHub logins that can sign in, separated by commas, for example `alice,bob`. Upper and lower case are the same. |
| `REVIEWQ_ADDR` | no | `127.0.0.1:8080` | The address and port where the server listens. |
| `REVIEWQ_DB` | no | `reviewqueue.db` | The path of the SQLite file. The server creates it. |

Make the secret key:

```sh
openssl rand -base64 32
```

Keep the secret key in a different place from the backups of the database file. A person who has both can read all tokens and keys. If you lose or change the key, the server cannot read the stored tokens and keys.

If a required variable is missing or wrong, the server stops at once with a message that names the variable.

## Build and run

You need Go. Run all commands in this directory.

```sh
go vet ./... && go test ./...
go build ./cmd/reviewqueue
REVIEWQ_SECRET_KEY='<key>' REVIEWQ_ALLOWED_LOGINS=alice,bob ./reviewqueue
```

The server has no C dependencies. To build for a Linux server on a Mac:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/reviewqueue
```

The server stops on SIGINT or SIGTERM. It cancels the running AI reviews. Finished reviews stay in the database.

## TLS

The server itself does not use TLS. GitHub tokens and session tokens go over the network, so never make the plain HTTP port reachable from other computers. Use one of these two setups:

- Keep the default address `127.0.0.1:8080`, and put a reverse proxy with TLS in front of the server.
- Listen only on a [Tailscale](https://tailscale.com) address. Tailscale encrypts the traffic between your devices.

This [Caddy](https://caddyserver.com) configuration gets a certificate for the domain and sends all requests to the server. `flush_interval -1` sends live events to the clients at once.

```caddyfile
reviewqueue.example.com {
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

The server limits sign-ins to 10 a minute for each client IP address. Behind a reverse proxy, the server sees only the address of the proxy, so all users share this limit.

## API

All requests and responses use JSON. An error response has the form `{"error": "text"}`. Each request except `POST /api/session` needs the header `Authorization: Bearer <sessionToken>`.

| Route | Purpose |
| --- | --- |
| `POST /api/session` | Sign in with `{"githubToken"}`. Returns `{"sessionToken", "login"}`. |
| `DELETE /api/session` | Sign out. |
| `GET /api/me` | Returns `{"login"}`. |
| `GET /api/settings`, `PUT /api/settings` | `repo`, `model`, and `hasOpenRouterKey`. `PUT` also takes `openRouterKey`. An empty key deletes it. |
| `GET /api/repos` | The repositories of the user. |
| `GET /api/models` | The OpenRouter models that support structured outputs. |
| `GET /api/review-requests` | The result of the last check of the selected repository. |
| `GET /api/repos/{owner}/{name}/pulls/{number}` | The details of a PR. |
| `GET /api/repos/{owner}/{name}/pulls/{number}/review` | The latest stored AI review of the PR, or 404. |
| `POST /api/repos/{owner}/{name}/pulls/{number}/review` | Start an AI review. Returns the task, or 409 if a review of the PR is already running. |
| `GET /api/tasks` | The running tasks of the user. |
| `DELETE /api/tasks/{id}` | Stop a task. |
| `GET /api/events` | A stream of Server-Sent Events: `requests`, `tasks`, and `review`. |
