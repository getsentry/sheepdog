# Sheepdog server

The Sheepdog server finds the open pull requests (PRs) in one GitHub repository where a review is requested from you or from one of your teams. It also runs AI reviews of PRs and gives PRs a danger score from 1 to 10 with [OpenRouter](https://openrouter.ai). The interface is a web app in `../web`. The server serves it at its own address.

The server serves several users. Each user signs in with an own GitHub token and has an own OpenRouter key, model, repository, reviews, and classifications. The server checks GitHub every 30 seconds for each user who has selected a repository, also when no client is connected. A user can turn this auto refresh off for a repository and check on demand. The server keeps its data in one SQLite file. It encrypts the GitHub tokens and the OpenRouter keys in that file.

## Create a GitHub token

Each user signs in with a classic personal access token (PAT). Fine-grained tokens do not work.

1. Open [the new token page](https://github.com/settings/tokens/new?scopes=repo,read:org&description=Sheepdog%20Token). The link selects the scopes `repo` and `read:org`. The server rejects a token without these two scopes.
2. Set an expiration date. A token without an expiration date stays valid until you delete it.
3. Click "Generate token" and copy the token.
4. If your organization uses SAML single sign-on (SSO), open [the token list](https://github.com/settings/tokens), click "Configure SSO" next to the token, and click "Authorize" for the organization. Without this step, GitHub does not show the data of that organization. The server then shows an error with the authorization link from GitHub.

The web app sends the token to the server once. To stop the access of the server, delete the token on GitHub.

## Sessions

The server knows a user by the numeric GitHub user ID, not by the login. If a user changes the login, the data stays with the user at the next sign-in. If another person later takes the old login, that person gets a new, empty user.

A sign-in gives the client a session token. It also sets the session token as the cookie `sheepdog_session` for the web app. Each device has its own session, and a new sign-in does not end the sessions on other devices. A session ends after 30 days without a request. The server deletes ended sessions at start and every hour.

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

You need Go 1.27.1 or later, and Node.js 22 or later for the web app. Run these commands in the root of the repository. The first command builds the web app into `server/internal/webui/dist`, and the second command embeds it into the `sheepdog` binary.

```sh
(cd web && npm ci && npm run build)
(cd server && go build ./cmd/sheepdog)
(cd server && SHEEPDOG_SECRET_KEY='<key>' SHEEPDOG_ALLOWED_LOGINS=alice,bob ./sheepdog)
```

Open the server address in a browser, for example `http://127.0.0.1:8080`, and sign in with your GitHub token.

Run the tests of the server in this directory:

```sh
go vet ./... && go test ./...
```

The server has no C dependencies. To build for a Linux server on a Mac:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/sheepdog
```

The server stops on SIGINT or SIGTERM. It cancels the running reviews and classifications. Finished ones stay in the database. A request that waits for GitHub or OpenRouter at that time gets status 503 with the text `The server is shutting down`.

## Web app

The server serves the web app at `/` and at every path that does not start with `/api/`. The `sheepdog` binary contains the web app: `go build` embeds the files in `internal/webui/dist` into it. Git keeps only `internal/webui/dist/.gitkeep`, so a fresh checkout builds without the web app. Then the server answers these paths with status 503 and a short page that says how to build the web app.

A path that is not a file gets `index.html`, so that the routes of the web app work after a reload. A missing file in `assets/` gets status 404. The files in `assets/` have names with a content hash, so they get `Cache-Control: public, max-age=31536000, immutable`. All other files, also `index.html`, get `Cache-Control: no-cache`.

Every web response has these headers:

- `Content-Security-Policy: default-src 'self'; connect-src 'self'; img-src 'self' data: https://avatars.githubusercontent.com; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'`
- `X-Content-Type-Options: nosniff`
- `Referrer-Policy: no-referrer`

### Develop the web app

Run these commands in `../web`:

```sh
npm ci
npm run dev
```

`npm run dev` starts the Vite development server at `http://localhost:5173`. It reloads the page when you change a file, and it sends every `/api` request to a server at `http://127.0.0.1:8080`. To use a server on another address, set `SHEEPDOG_API_URL`, for example `SHEEPDOG_API_URL=http://127.0.0.1:9090 npm run dev`. The development server keeps the `Host` header of the browser, so the `Origin` check of the server passes.

The checks of the web app:

```sh
npm run typecheck
npm test
npm run e2e
```

`npm test` runs the unit tests with Vitest. `npm run e2e` builds the web app and runs a Playwright test in Chromium against mocked `/api` routes. It needs no server and no GitHub token, and it saves screenshots in the folder `sheepdog-screenshots` in the temporary folder of the system, or in `SHEEPDOG_SCREENSHOTS`. Run `npx playwright install chromium` once before the first run.

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

The session cookie has `Secure` only when the request came over HTTPS. The server itself does not use TLS, so it trusts the header `X-Forwarded-Proto: https` from a reverse proxy on the same computer. The server ignores the header on other connections. The reverse proxy must also keep the `Host` header of the browser, because the server compares the `Origin` header with it. Caddy does both by default.

After 10 failed sign-ins in a minute from one client IP address, the server rejects sign-ins from that address with status 429 for the rest of the minute. Successful sign-ins do not count. If the connection comes from a loopback address, for example from a reverse proxy on the same computer, the server uses the last address in the `X-Forwarded-For` header as the client address. Caddy sets this header by default. The server ignores the header on other connections, for example on a Tailscale address.

## Review lanes

Sheepdog works only with repositories that have the file `.github/sheepdog.yml` on the default branch. The file sorts the review requests into lanes. The server reads the lanes from the base branch of each PR, so a PR cannot change its own lane. A PR whose base branch has no file gets a row without a lane.

```yaml
version: 1
lanes:
  safe:
    authors: ["dependabot[bot]"]
    paths: ["package.json", "package-lock.json"]
    max_changed_lines: 50
    max_score: 2
    no_major_bumps: true
    require_checks: true
    action: approve-and-automerge
    add_labels: [ready-to-merge]
never_automatic:
  paths: [".github/**", "src/sentry.ts"]
classification:
  model: anthropic/claude-sonnet-4.5
  ci: true
  comment: true
```

- `version` must be `1`. The server rejects unknown keys.
- A PR goes to the first lane, in file order, whose conditions all match. A PR that matches no lane is in the lane `manual`. You cannot use `manual` as a lane name.
- Every lane field is optional. A missing field sets no condition.
- `authors` matches the login of the PR author. Upper and lower case are the same. An entry that ends with `[bot]`, for example `dependabot[bot]`, matches only a GitHub App author with that name, so that a person with the login `dependabot` does not match. An entry without `[bot]` matches any author with that login. In YAML, write an entry with `[bot]` in quotes.
- `paths` matches when every changed file matches one of the globs. `**` matches any number of directories.
- `max_changed_lines` is the highest number of added plus deleted lines.
- `max_score` is the highest classification score. Only a classification of the current head commit counts. Click "Classify" in the web app, turn on [auto classify](#auto-classify), or classify in CI. See [Classification in CI](#classification-in-ci).
- `no_major_bumps: true` matches a title like `Bump expo from 57.0.1 to 57.2.0`, where the first number of both versions is the same. A title without this form does not match.
- `require_checks: true` matches when all checks of the head commit passed. A PR without checks does not match.
- `action` is `suggest` (the default) or `approve-and-automerge`.
- `add_labels` are the labels that "approve and merge" adds before it approves.
- `never_automatic.paths` puts a PR in `manual` when one changed file matches one of the globs. A PR with more than 100 changed files is also in `manual`.
- `classification` is optional. If you set it, `model` is required.
- `classification.model` is the OpenRouter model for every classification of the repository. A click on "Classify" in the web app uses this model instead of the model of the user, and the stored classification names this model. If the rules file is missing or not valid, the server uses the model of the user.
- `classification.ci: true` tells the server to read the score of the Sheepdog workflow for each PR. See [Classification in CI](#classification-in-ci).
- `classification.comment: true` tells the Sheepdog workflow to post the score as a comment on the PR. The default is `false`. See [Comment on the PR](#comment-on-the-pr).

Each row has notes that explain its lane. For a PR in an `approve-and-automerge` lane, the notes list the conditions that matched. For every other PR, the notes list the conditions that failed in the closest `approve-and-automerge` lane, for example `Needs a classification of the current commit`.

After a classification is stored, the server checks GitHub again at once, so the lane changes without a wait of 30 seconds.

`POST /api/repos/{owner}/{name}/pulls/{number}/approve-and-merge` takes `{"headSha"}`, the head commit that the user saw. The server reads the PR, the rules, and the classification again. With `classification.ci: true`, it also reads the CI score again. Then it does these steps:

1. It returns 404 if the PR is not in the last list of review requests of the user.
2. It returns 409 if the head commit is not `headSha`, if the rules file is missing or not valid, or if the PR is not in an `approve-and-automerge` lane. The error text names the reason. The server changes nothing on GitHub in these cases.
3. It adds the `add_labels` of the lane, and approves the head commit with the token of the user.
4. It waits at most 3 seconds until GitHub knows the merge state. If the PR can be merged now, the server merges it, and only if its head is still the approved commit. It uses squash if the repository allows it, then a merge commit, then rebase. Otherwise it turns on auto-merge, and returns 409 if the repository does not allow auto-merge. The labels and the approval stay in that case, and the error says so.
5. It returns `{"result": "merged"}` or `{"result": "auto-merge"}`, and checks GitHub again at once.

A GitHub error, for example for an approval of your own PR, gives status 502 with the GitHub text.

### Set up a repository

At each poll, the server first reads `.github/sheepdog.yml` from the default branch of the selected repository. If the file is missing, the server does not search for review requests. The `requests` result then has empty `rows` and `lanes`, and `setup`:

```json
{"defaultBranch": "main", "pullRequest": {"number": 12, "url": "https://github.com/acme/app/pull/12"}}
```

`pullRequest` is the open PR from the branch `sheepdog/setup` to the default branch, or `null`. After that PR is merged, the next poll shows the review requests. The poll interval stays 30 seconds.

The routes for a PR, its review, its classification, the classifications of the repository, and approve and merge return 409 with the text `<owner/name> has no .github/sheepdog.yml on <branch>. Set it up first` for a repository without the file. The server keeps the result of this check for each user and repository for 30 seconds.

`GET /api/repos/{owner}/{name}/setup/template` returns `{"content"}`, a rules file to start with. It sets `classification.model` to the model of the user, or has a comment instead when the user has no model. Its lane `safe` approves and merges small Dependabot updates without a major version change, with green checks and a score of at most 2.

`POST /api/repos/{owner}/{name}/setup` takes `{"content"}` and opens a PR that adds the file:

1. It returns 400 with the error of the parser if the content is not a valid rules file.
2. It returns 409 if the default branch already has the file, or if the branch `sheepdog/setup` already exists. The text then has the link of the open PR of that branch. If the branch has no open PR, delete the branch on GitHub and try again.
3. It creates the branch `sheepdog/setup` from the head of the default branch, commits `.github/sheepdog.yml` with the message `chore: add Sheepdog rules`, and opens the PR `Add Sheepdog rules` with the token of the user.
4. It returns 201 with `{"number", "url"}` of the PR, and checks GitHub again at once.

The server never commits to the default branch, because rulesets in many repositories require a PR for every change there. The PR needs a review like every other PR of the repository. A GitHub error, for example for a user without write access, gives status 502 with the GitHub text. The server cannot add the workflow `.github/workflows/sheepdog.yml`, because a token needs the scope `workflow` for files in `.github/workflows`. The template explains how to add it.

## Auto classify

Auto classify is off for each user and repository until the user turns it on with `PUT /api/repos/{owner}/{name}/settings` and `{"autoClassify": true}`, or with the "Auto-classify" switch in the repository settings of the web app. Each classification is a request to OpenRouter with the key of the user, so the user pays for it.

After each poll, the server then classifies a PR only when all of these conditions are true:

1. The user has an OpenRouter key and a model in Settings.
2. A classification of the head commit is the only missing condition of an `approve-and-automerge` lane before the lane of the PR. For example, the server does not classify a PR with too many changed lines or with checks that are not green.
3. The PR is not a draft.
4. The rules file on the base branch does not have `classification.ci: true`. Then the score comes from CI.
5. The server did not start a classification of this head commit for the user before. A failed or stopped classification runs again only after a new commit, or after a restart of the server.
6. The user has fewer than 2 classifications that run. The server starts the next one at the next poll.

The classifications show in the running tasks, and the user can stop them. Auto classify does not approve or merge. "Approve and merge" still needs a click. When the user turns auto classify on, the server checks GitHub at once.

## Classification in CI

A repository can classify every PR in GitHub Actions. Then the score is ready without a click, and every user sees a score from the same model. The model is `classification.model` in `.github/sheepdog.yml`.

The command `sheepdog classify --pr <number>` classifies one PR. It reads these environment variables:

| Variable | Value |
| --- | --- |
| `GITHUB_TOKEN` | A token that can read the PR and the contents of the repository. |
| `OPENROUTER_API_KEY` | The OpenRouter API key. |
| `GITHUB_REPOSITORY` | The repository, for example `getsentry/sentry-react-native`. GitHub Actions sets it. |
| `GITHUB_API_URL` | Optional. The GitHub API address. GitHub Actions sets it. The default is `https://api.github.com`. |
| `GITHUB_STEP_SUMMARY` | Optional. GitHub Actions sets it. The command adds a short Markdown summary of the score to this file. |

The command does not read the server configuration. It reads `.github/sheepdog.yml` and `AGENTS.md` from the base branch of the PR, and it classifies the current head commit with `classification.model`. If the rules file is missing, is not valid, or has no `classification.model`, the command stops with a message. On success, it prints one line:

```text
::notice title=sheepdog-classification::{"score":2,"reason":"...","model":"...","headSha":"...","diffTruncated":false}
```

GitHub reads this line as a workflow command and adds a notice annotation to the job. The command writes `%`, CR, and LF in the JSON as `%25`, `%0D`, and `%0A`, as GitHub requires. On an error, the command stops with exit code 1 and prints the error. It never prints the token or the key.

The composite action `getsentry/sheepdog/ci` builds `sheepdog` from its own source and runs the command for the PR of the event. It has two inputs:

- `openrouter-api-key` is required.
- `github-token` is optional. The default is the token of the workflow run.

The action does not check out the code of the PR. Add this workflow to the repository as `.github/workflows/sheepdog.yml`. The server trusts no other file name. Replace `<commit>` with a full commit SHA of `getsentry/sheepdog`.

```yaml
name: Sheepdog
on:
  pull_request_target:
permissions:
  contents: read
  pull-requests: read
concurrency:
  group: sheepdog-${{ github.event.pull_request.number }}
  cancel-in-progress: true
jobs:
  classify:
    name: Sheepdog classification
    runs-on: ubuntu-latest
    steps:
      - uses: getsentry/sheepdog/ci@<commit>
        with:
          openrouter-api-key: ${{ secrets.OPENROUTER_API_KEY }}
```

### Comment on the PR

With `classification.comment: true` on the base branch, the command also posts the score as a comment on the PR after it prints the annotation. It keeps one comment for each PR. The comment starts with `<!-- sheepdog-classification -->`, and on the next commit the command edits its own comment from `github-actions[bot]` and posts no new one. The comment shows the score, a short danger label, the reason of the model as a quote, the model, and the commit. The command breaks `@` mentions in the reason, so that the text of the model cannot notify people.

The comment needs write access to pull requests. Change the permissions of the workflow to:

```yaml
permissions:
  contents: read
  pull-requests: write
```

If the command cannot post the comment, it prints a warning, and the job still passes, because the score is already in the annotation. The server never reads the comment. Anyone can write a comment, so only the annotation counts for the lanes.

Add `OPENROUTER_API_KEY` as an Actions secret of the repository. Workflows that a Dependabot PR starts get only Dependabot secrets, so add the key also as a Dependabot secret. The owner of the key pays for the OpenRouter requests. Everyone who can read the repository can see the score in the check of the PR.

GitHub runs a `pull_request_target` workflow from the base branch, so a PR cannot change the workflow or the version of the action. With `classification.ci: true` on the base branch, the server reads the GitHub Actions check suites of the head commit of each PR at each poll. It uses a score only when all of these conditions are true:

1. The check run has the name `Sheepdog classification`.
2. The workflow run has the event `pull_request_target` and the file `.github/workflows/sheepdog.yml`.
3. The annotation has the title `sheepdog-classification` and valid JSON with a score from 1 to 10.
4. The `headSha` of the annotation is the current head commit of the PR.

For the lanes, the server uses the CI score of the head commit. If there is no CI score, it uses the stored app classification of the head commit. Without `classification.ci: true`, the server does not read check suites.

## API

All requests and responses use JSON. An error response has the form `{"error": "text"}`. An unknown route under `/api/` gets status 404. Each request except `POST /api/session` needs the header `Authorization: Bearer <sessionToken>` or the cookie `sheepdog_session`. If a request has both, the server uses the header. A missing, unknown, or ended session gets status 401. A login that is no longer in `SHEEPDOG_ALLOWED_LOGINS` gets status 403.

The browser sends the cookie by itself, so the server protects cookie requests against other sites. The cookie has `HttpOnly`, `SameSite=Strict`, and `Path=/`. A cookie request with a method other than `GET` or `HEAD` gets status 403 unless its `Origin` header is the origin of the server, for example `https://sheepdog.example.com`. A request without an `Origin` header gets 403 too. `POST /api/session` with an `Origin` header of another origin, also `null`, gets status 403. Requests with the `Authorization` header do not need an `Origin` header.

The routes under `/api/repos/{owner}/{name}/pulls/` and `/api/repos/{owner}/{name}/classifications` return 409 for a repository without `.github/sheepdog.yml` on its default branch.

The web app shows the starred repositories first in the repository picker. Sheepdog keeps the stars in its own database for each user, not on GitHub, because a star on GitHub is public and has another meaning. A star does not need `.github/sheepdog.yml`.

Auto refresh is on for each user and repository until the user turns it off with `PUT /api/repos/{owner}/{name}/settings` and `{"autoRefresh": false}`. Then the server does not check GitHub every 30 seconds while the user has this repository selected. It still checks at once when the user selects the repository, asks for a refresh, stores a classification, approves and merges a PR, or opens a setup PR. When the user turns auto refresh on again for the selected repository, the server checks at once and then every 30 seconds. The server keeps the repository settings in the table `repo_prefs`. A repository without a row there has auto refresh on and auto classify off. The web app has a refresh button and a "Repository settings" button at the bottom of the sidebar.

GitHub repository names ignore case. The server changes them to lower case, so settings, stars, review requests, tasks, events, stored reviews, and stored classifications always use lower case names.

A review or a classification sends at most 200,000 characters of the diff. The server counts Unicode code points, not bytes and not visible characters. GitHub responses can have at most 50 MB for a diff and 10 MB for other data, and OpenRouter responses at most 10 MB. A larger response gives an error.

| Route | Purpose |
| --- | --- |
| `POST /api/session` | Sign in with `{"githubToken"}`. Returns `{"sessionToken", "login"}`, and sets the session cookie. |
| `DELETE /api/session` | Sign out. Ends this session, closes its event streams, and deletes the session cookie. Returns 204. |
| `DELETE /api/sessions` | Sign out on all devices. Ends every session of the user, closes all its event streams, and deletes the session cookie. Returns 204. |
| `GET /api/me` | Returns `{"login"}`. |
| `GET /api/settings`, `PUT /api/settings` | `repo`, `model`, `hasOpenRouterKey`, and `starred`. `PUT` takes `repo`, `model`, and `openRouterKey`. An empty key deletes it. `starred` is the list of starred repositories in name order, and an empty list when there is none. |
| `GET /api/repos` | The repositories of the user. |
| `GET /api/repos/{owner}/{name}/settings`, `PUT /api/repos/{owner}/{name}/settings` | The settings of the user for the repository: `repo`, `autoRefresh`, and `autoClassify`. `PUT` takes `autoRefresh` and `autoClassify`, and returns the new settings. A repository without `.github/sheepdog.yml` has settings too. |
| `PUT /api/repos/{owner}/{name}/star`, `DELETE /api/repos/{owner}/{name}/star` | Add or remove a star of the repository. Returns the settings. A second `PUT` or `DELETE` changes nothing. A user can star at most 200 repositories. One more star gets status 409. |
| `GET /api/models` | The OpenRouter models that support structured outputs. |
| `GET /api/review-requests` | The result of the last check of the selected repository: `repo`, `fetchedAt`, `rows`, `lanes`, `defaultBranch` after the first successful check, and the optional `error`, `rulesError`, and `setup`. `setup` is set only when the default branch has no rules file. See [Set up a repository](#set-up-a-repository). Each row has `headSha`, and a `lane` with `name`, `action`, and `notes` when the repository has a valid rules file. A row with a CI score has `classification` with `source` (always `ci`), `score`, `reason`, `model`, and `headSha`. `lanes` lists the lane names in file order, without `manual`. `rulesError` explains why the rules file is not valid. |
| `POST /api/review-requests/refresh` | Check GitHub at once. Returns 202, and the result comes as a `requests` event. More calls during a check lead to only one more check. Returns 409 when no repository is selected, or when the server stopped the checks after GitHub rejected the token. |
| `GET /api/repos/{owner}/{name}/pulls/{number}` | The details of a PR. |
| `GET /api/repos/{owner}/{name}/pulls/{number}/review` | The latest stored AI review of the PR, or 404. |
| `POST /api/repos/{owner}/{name}/pulls/{number}/review` | Start an AI review. Returns the task, or 409 if a review of the PR is already running. |
| `GET /api/repos/{owner}/{name}/classifications` | The latest stored classification of each PR of the repository, ordered by PR number. An empty list when there is none. |
| `GET /api/repos/{owner}/{name}/pulls/{number}/classification` | The latest stored classification of the PR, with a `score` from 1 (no danger, can be merged) to 10 (extreme danger) and a `reason`, or 404. |
| `POST /api/repos/{owner}/{name}/pulls/{number}/classification` | Start a classification. Returns the task, or 409 if a classification of the PR is already running. A review and a classification of one PR can run at the same time. |
| `POST /api/repos/{owner}/{name}/pulls/{number}/approve-and-merge` | Approve and merge a PR of an `approve-and-automerge` lane. See [Review lanes](#review-lanes). |
| `GET /api/repos/{owner}/{name}/setup/template` | A rules file to start with. See [Set up a repository](#set-up-a-repository). |
| `POST /api/repos/{owner}/{name}/setup` | Open a PR that adds the rules file. See [Set up a repository](#set-up-a-repository). |
| `GET /api/tasks` | The running tasks of the user. |
| `DELETE /api/tasks/{id}` | Stop a task. |
| `GET /api/events` | A stream of Server-Sent Events: `requests`, `tasks`, `review`, and `classification`. A new stream of a session ends the older streams of that session. A user can open at most 8 streams at a time. One more stream gets status 429. |

A `requests` or `tasks` event has the full current state. A `review` or `classification` event is sent once when the result is stored or fails, also when the task was stopped after the result was stored. The server never skips these events. If a client reads its stream too slowly, the server ends that stream instead, and the client must connect again. A new stream starts with the current `requests` and `tasks` events, and the client must load the stored reviews and classifications again with `GET .../review` and `GET .../classification`.
