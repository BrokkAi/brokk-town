#!/bin/sh
# Install a published release without a Go toolchain.
set -eu

fail() {
    printf 'brokk-town installer: %s\n' "$*" >&2
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

    for tool in curl tar awk mktemp uname python3; do
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
        registry=$(download "https://registry.npmjs.org/@brokkai/brokk-town/latest") || fail 'could not resolve Town version'
        version=$(printf '%s' "$registry" | python3 -c 'import json,sys; print("v"+json.load(sys.stdin)["version"]+"-town")') || fail 'invalid Town version'
    fi
    printf '%s\n' "$version" | awk '
        /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$/ { valid++ }
        END { exit !(NR == 1 && valid == 1) }
    ' || fail 'version must be a tag such as v0.1.0 or v0.1.0-rc.1'

    case "$version" in *-town) ;; *) version=$version-town ;; esac
    install_dir=${INSTALL_DIR:-${HOME:?set HOME or INSTALL_DIR}/.local/bin}
    case "$install_dir" in
        /*) ;;
        *) install_dir=$PWD/$install_dir ;;
    esac
    [ ! -d "$install_dir/bt" ] || fail 'installation target is a directory'
    temporary=$(mktemp -d)
    staged=
    trap 'rm -rf "$temporary"; if [ -n "$staged" ]; then rm -rf "$staged"; fi' 0
    trap 'exit 1' HUP INT TERM

    asset=brokk-town-$version-$os-$arch.tar.gz
    printf 'Downloading brokk-town %s for %s/%s...\n' "$version" "$os" "$arch"
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
    mkdir -p "$install_dir/.brokk-town"
    staged=$(mktemp -d "$install_dir/.brokk-town/$version.XXXXXX")
    python3 - "$temporary/$asset" "$staged" <<'EXTRACT'
import pathlib, sys, tarfile
with tarfile.open(sys.argv[1], 'r:gz') as archive:
    expected = {'bt', 'README.md', 'BUILD.json', 'LICENSE', 'NOTICE', 'licenses/THIRD_PARTY_NOTICES.txt'}
    members = archive.getmembers()
    names = {m.name for m in members}
    if len(names) != len(members) or names != expected:
        raise ValueError('unexpected Town archive contents')
    for member in members:
        name = pathlib.PurePosixPath(member.name)
        if not member.isfile() or name.is_absolute() or '..' in name.parts:
            raise ValueError('unsafe bundle entry')
        target = pathlib.Path(sys.argv[2]) / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(archive.extractfile(member).read())
        target.chmod(0o755 if member.name == 'bt' else 0o644)
EXTRACT
    ln -s "$staged/bt" "$staged/entrypoint"
    mv -f "$staged/entrypoint" "$install_dir/bt"
    staged=
    printf 'Installed brokk-town %s to %s/bt\n' "$version" "$install_dir"
    case ":${PATH:-}:" in
        *":$install_dir:"*) ;;
        *) printf 'Add %s to your PATH, then run bt --help.\n' "$install_dir" ;;
    esac
}

main "$@"
