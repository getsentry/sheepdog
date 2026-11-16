# Review Queue

A macOS app that shows the open pull requests in one GitHub repository where a review is requested from you or from one of your teams.

The app is a client of the Review Queue server. It gets all data from the server and does not call GitHub or OpenRouter. Start a server first. See [server/README.md](../server/README.md).

Run all commands in this directory.

Build and test:

```sh
swift build
swift test
```

Make the app bundle at `build/ReviewQueue.app` and start it:

```sh
scripts/bundle.sh
open build/ReviewQueue.app
```

To sign in, enter the server address, for example `http://127.0.0.1:8080`, and a GitHub classic personal access token with the scopes `repo` and `read:org`. The "Create token on GitHub" button opens the GitHub page for a new token with these scopes. The app sends the token to the server once and does not store it.

The app stores the session token of the server in the Keychain (service `com.alwx.reviewqueue`, account `session-token`) and the server address in user defaults (`serverURL`). To use another server, click "Sign out" in Settings and enter the new address on the sign-in screen.
