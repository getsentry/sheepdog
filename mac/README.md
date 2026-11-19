# Sheepdog

A macOS app that shows the open pull requests in one GitHub repository where a review is requested from you or from one of your teams.

The app is a client of the Sheepdog server. It gets all data from the server and does not call GitHub or OpenRouter. Start a server first. See [server/README.md](../server/README.md).

A repository can sort its review requests into lanes with the file `.github/sheepdog.yml`. See "Review lanes" in [server/README.md](../server/README.md). When the server sends lanes, the list shows one section for each lane in the order of the file, then the section `manual`. Each section header shows the lane name and the number of PRs. A section without PRs is not shown. A repository without the file gives one list without sections. If the file is not valid, the error shows above the list.

Each row and the detail panel show the notes of the lane. The notes tell why a PR is in its lane, or what it needs to be in a lane with automatic merge, for example "Needs a classification of the current commit".

Each row and the detail panel show the danger score of the PR from 1 to 10. A repository can classify each PR in GitHub Actions. See "Classification in CI" in [server/README.md](../server/README.md). If the server sends a CI score for the current head commit, the app shows it, and the detail panel shows "From CI" and the model. If there is no CI score, the app shows the classification that you started with "Classify". When the PR changed after that classification, the score is dimmed. The "Classify" button always starts a new classification in the app, also when a CI score exists. The CI score stays visible while it exists.

For a lane with the action `approve-and-automerge`, the section header has the button "Approve and merge (N)", and the detail panel of each PR in that lane has the button "Approve and merge". Each button opens a confirmation that lists the PRs by number and title. The app sends nothing before you confirm. Then the server approves each PR with your GitHub account, and merges it or turns on auto-merge. The app sends one PR after another, with the head commit that you saw. If a PR fails, the other PRs still run, and the error above the list names each failed PR with the GitHub text.

Run all commands in this directory.

Build and test:

```sh
swift build
swift test
```

Make the app bundle at `build/Sheepdog.app` and start it:

```sh
scripts/bundle.sh
open build/Sheepdog.app
```

To sign in, enter the server address and a GitHub classic personal access token. The address field starts with `http://127.0.0.1:8080`, the default address of the server, until you sign in to another one. Use a token with the scopes `repo` and `read:org`. The "Create token on GitHub" button opens the GitHub page for a new token with these scopes. The app sends the token to the server once and does not store it.

The server address must start with `https://`, for example `https://reviews.example.com` or `https://box.tailnet.ts.net`. Plain `http://` works only for `localhost` and IP addresses, for example `http://127.0.0.1:8080` or the Tailscale IP address of the server, because macOS blocks plain HTTP to host names. See the TLS section of [server/README.md](../server/README.md).

The app stores the session token of the server in the Keychain (service `com.alwx.sheepdog`, account `session-token`) and the server address in user defaults (`serverURL`). To use another server, click "Sign out" in Settings and enter the new address on the sign-in screen. "Sign out on all devices" ends every session of your account on the server, for example after you lose a device.
