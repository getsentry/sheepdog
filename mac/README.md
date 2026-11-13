# Review Queue

A macOS app that shows the open pull requests in one GitHub repository where a review is requested from you or from one of your teams. It uses the [GitHub CLI](https://cli.github.com) (`gh`) for all GitHub access. Install it with `brew install gh`.

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

If `gh` has no GitHub login, the app shows a "Sign in with GitHub" button. To try the login screen without a change to your `gh` login, start the app with an empty `gh` configuration directory:

```sh
GH_CONFIG_DIR=$(mktemp -d) build/ReviewQueue.app/Contents/MacOS/ReviewQueue
```
