# Licensing and third-party notices

Brokk Town uses [Apache-2.0](../LICENSE). [NOTICE](../NOTICE) identifies the project
and adapted code. [THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt) is generated,
not hand-written.

```sh
sh scripts/licenses.sh
```

It reproduces the project license and notice, then every legal file of every
module in the build, then the supplementary texts below. GoReleaser runs the same
script before packaging, and CI fails if the committed file is not current.

The script only collects text. It does not classify or approve licenses, so a
dependency change is reviewed by reading its license in the generated diff.

## Supplementary texts

`GO_LICENSE.txt`, `GO_PATENTS.txt` and `UNICODE_LICENSE.txt` are not part of the
module graph: the Go runtime and the Unicode tables are not modules, so there is
no module directory to copy them from. They are appended to every generated
notice file because the release archives ship compiled binaries.

Original generated artwork is documented in [themes.md](../docs/themes.md).
Separately installed coding agents and interpreters have their own terms.
