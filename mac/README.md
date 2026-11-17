# Sheepdog

A macOS app that shows the open pull requests in one GitHub repository where a review is requested from you or from one of your teams.

The app is a client of the Sheepdog server. It gets all data from the server and does not call GitHub or OpenRouter. Start a server first. See [server/README.md](../server/README.md).

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
