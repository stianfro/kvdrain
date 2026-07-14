# Install kvdrain

kvdrain is alpha software. Read the release notes and run `kvdrain status NODE`
before a drain.

## Homebrew

After the `v0.1.0` tap is published, install on macOS or Linux with:

```sh
brew install stianfro/tap/kvdrain
```

Upgrade or remove it with `brew upgrade kvdrain` and `brew uninstall kvdrain`.
The tap repository is created as part of the release setup, not by this repository.

## Verified POSIX installer

The installer supports Linux and macOS on amd64 and arm64:

```sh
curl -fsSL https://github.com/stianfro/kvdrain/releases/latest/download/install.sh | sh
```

It installs to `${XDG_BIN_HOME:-$HOME/.local/bin}` by default. Set an exact tag or
destination when needed:

```sh
KVDRAIN_VERSION=v0.1.0 KVDRAIN_INSTALL_DIR="$HOME/bin" sh install.sh
```

It does not use `sudo` or edit shell profiles. It downloads the selected archive
and `checksums.txt`, requires one exact checksum entry, verifies SHA-256, extracts
only the top-level `kvdrain` binary, confirms its reported version, and atomically
replaces the destination. To upgrade, run it again. To uninstall, remove the
installed `kvdrain` file.

Review a downloaded copy of `install.sh` before running it.

## Krew

After the plugin is accepted into the Krew index:

```sh
kubectl krew install kvdrain
kubectl krew upgrade kvdrain
kubectl krew uninstall kvdrain
```

Krew exposes it as `kubectl kvdrain`. Automated Krew updates are disabled until
the repository variable `KREW_PUBLISH` is set to `true` after the initial index
entry is accepted.

## Windows and manual installation

Download the Windows amd64 or arm64 zip from the release page. Verify its checksum,
extract `kvdrain.exe`, and place it in a directory on `PATH`. The POSIX installer
does not run on Windows.

For any platform, download the archive and `checksums.txt`, then verify the exact
file before extraction:

```sh
sha256sum --check --ignore-missing checksums.txt
```

On macOS, use `shasum -a 256 ARCHIVE` and compare it with the exact archive entry.
Stable archive names include the tag, for example
`kvdrain_v0.1.0_linux_amd64.tar.gz`.

## Provenance

Releases include checksums, SPDX SBOMs, Sigstore bundles, and GitHub build
provenance. Verify an archive with:

```sh
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/stianfro/kvdrain/\.github/workflows/release\.yml@refs/tags/v[0-9].*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
gh attestation verify kvdrain_v0.1.0_linux_amd64.tar.gz --repo stianfro/kvdrain
```

The checksum attestation includes `install.sh` because the release checksum file
covers that asset. Source-build instructions are in [CONTRIBUTING.md](../CONTRIBUTING.md).
