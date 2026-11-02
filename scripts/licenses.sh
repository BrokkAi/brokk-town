#!/bin/sh
# Generate the license file shipped with a release: project terms plus every
# legal file of every module in the build.
set -eu
export LC_ALL=C

out=${1:-licenses/THIRD_PARTY_NOTICES.txt}

{
    printf 'BROKK TOWN LICENSES\n\n%s\n\n' '=============================================================='
    printf 'Project license (LICENSE)\n\n'
    cat LICENSE
    printf '\n%s\n\n' '=============================================================='
    printf 'Project notice (NOTICE)\n\n'
    cat NOTICE
} >"$out"

go mod download all
go list -m -f '{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}' all |
while read -r path version dir; do
    printf '\n%s\n\n%s %s\n\n' '==============================================================' "$path" "$version" >>"$out"
    find "$dir" -maxdepth 1 -type f \
        \( -iname 'LICENSE*' -o -iname 'LICENCE*' -o -iname 'COPYING*' \
        -o -iname 'NOTICE*' -o -iname 'PATENTS*' \) | sort |
    while read -r file; do
        printf '%s\n\n' "${file#"$dir"/}" >>"$out"
        cat "$file" >>"$out"
        printf '\n' >>"$out"
    done
done

supplemental() {
    printf '\n%s\n\n%s\n\n' '==============================================================' "$1" >>"$out"
    cat "$2" >>"$out"
    printf '\n' >>"$out"
}

supplemental 'Go runtime and standard library (BSD-3-Clause)' licenses/GO_LICENSE.txt
supplemental 'Go additional patent grant' licenses/GO_PATENTS.txt
supplemental 'Unicode data used by Go and uniseg (Unicode-3.0)' licenses/UNICODE_LICENSE.txt

printf 'Wrote %s\n' "$out"
