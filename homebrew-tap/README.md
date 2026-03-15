# Homebrew Tap for Belay

This directory contains reference material for the Homebrew tap used to distribute Belay.

The actual tap repository lives at [github.com/davidparkercodes/homebrew-tap](https://github.com/davidparkercodes/homebrew-tap). GoReleaser automatically updates the formula in that repository on each tagged release.

## Installation

```bash
brew install davidparkercodes/tap/belay
```

Or add the tap first, then install:

```bash
brew tap davidparkercodes/tap
brew install belay
```

## How It Works

The `.goreleaser.yml` in the Belay repo includes a `brews` section that tells GoReleaser to push a Homebrew formula to the `davidparkercodes/homebrew-tap` repository whenever a new release is created. The formula is auto-generated with the correct download URLs, checksums, and version info.

No manual formula maintenance is required -- just tag a release and GoReleaser handles the rest.
