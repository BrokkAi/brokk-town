#!/bin/sh
# Install a published release without a Go toolchain.
set -eu

fail() {
    printf 'brokk-simplifier-bot installer: %s\n' "$*" >&2
    exit 1
}

download() {
    curl --fail --silent --show-error --location --retry 3 \
        --proto '=https' --proto-redir '=https' "$@"
}

main() {
    if [ "$#" -gt 1 ]; then
        fail 'usage: sh install.sh [VERSION]; set INSTALL_DIR to change the destination'
    fi
    case "${1:-}" in
        -h|--help)
            printf 'Usage: sh install.sh [VERSION]\nDefaults: latest stable release, INSTALL_DIR=$HOME/.local/bin\n'
            return
            ;;
    esac

    for tool in curl tar awk mktemp uname; do
        command -v "$tool" >/dev/null 2>&1 || fail "required command not found: $tool"
    done
    if command -v sha256sum >/dev/null 2>&1; then
        checksum_tool=sha256sum
    elif command -v shasum >/dev/null 2>&1; then
        checksum_tool=shasum
    else
        fail 'install sha256sum or shasum to verify downloads'
    fi

    case "$(uname -s)" in
        Linux) os=linux ;;
        Darwin) os=darwin ;;
        *) fail 'supported operating systems: Linux and macOS' ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) fail 'supported architectures: amd64 and arm64' ;;
    esac

    releases=https://github.com/BrokkAi/brokk-town/releases
    version=${1:-latest}
    if [ "$version" = latest ]; then
        command -v python3 >/dev/null 2>&1 || fail 'python3 is required to resolve the latest version'
        registry=$(download "https://registry.npmjs.org/@brokkai/simplifier-bot/latest") || fail 'could not resolve bot version'
        version=$(printf '%s' "$registry" | python3 -c 'import json,sys; print("v"+json.load(sys.stdin)["version"])') || fail 'invalid bot version'
    fi
    case "$version" in *-simplifier-bot) ;; *) version=$version-simplifier-bot ;; esac
    printf '%s\n' "$version" | awk '
        /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$/ { valid++ }
        END { exit !(NR == 1 && valid == 1) }
    ' || fail 'version must be a tag such as v0.1.0 or v0.1.0-rc.1'

    install_dir=${INSTALL_DIR:-${HOME:?set HOME or INSTALL_DIR}/.local/bin}
    case "$install_dir" in
        /*) ;;
        *) install_dir=$PWD/$install_dir ;;
    esac
    [ ! -d "$install_dir/bsb" ] || fail 'installation target is a directory'
    temporary=$(mktemp -d)
    staged=
    trap 'rm -rf "$temporary"; if [ -n "$staged" ]; then rm -f "$staged"; fi' 0
    trap 'exit 1' HUP INT TERM

    asset=brokk-simplifier-bot-$version-$os-$arch.tar.gz
    printf 'Downloading brokk-simplifier-bot %s for %s/%s...\n' "$version" "$os" "$arch"
    download --output "$temporary/$asset" "$releases/download/$version/$asset" || fail "could not download $asset"
    download --output "$temporary/checksums.txt" "$releases/download/$version/checksums.txt" || fail 'could not download checksums.txt'

    expected=$(awk -v asset="$asset" '$2 == asset { count++; hash = $1 } END { if (count != 1) exit 1; print hash }' "$temporary/checksums.txt") ||
        fail 'checksum must contain exactly one entry for the archive'
    if [ "$checksum_tool" = sha256sum ]; then
        actual=$(sha256sum "$temporary/$asset")
    else
        actual=$(shasum -a 256 "$temporary/$asset")
    fi
    actual=${actual%% *}
    [ "$actual" = "$expected" ] || fail "checksum mismatch for $asset"

    mkdir -p "$install_dir"
    staged=$(mktemp "$install_dir/.brokk-simplifier-bot.XXXXXX")
    # Extract only the binary to a fresh file; never follow archive paths or links.
    tar -xzOf "$temporary/$asset" bsb > "$staged" || fail 'could not extract bsb'
    [ -s "$staged" ] || fail 'archive contains an empty binary'
    chmod 755 "$staged"
    mv -f "$staged" "$install_dir/bsb"
    staged=
    printf 'Installed brokk-simplifier-bot %s to %s/bsb\n' "$version" "$install_dir"
    case ":${PATH:-}:" in
        *":$install_dir:"*) ;;
        *) printf 'Add %s to your PATH, then run bsb --help.\n' "$install_dir" ;;
    esac
}

main "$@"
