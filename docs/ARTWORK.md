# Town artwork

The two RGBA sprite atlases in `internal/web/assets` were generated specifically
for Brokk Town using OpenAI ImageGen on 2026-09-09. They depict six houses, two
wheelbarrow orientations, two truck orientations, and two workers. The artwork
is provided with this project's Apache-2.0 distribution. No external sprite pack
or munder-difflin asset is included.

The idea of visible agents working in a shared scene was inspired by
[munder-difflin](https://github.com/chaitanyagiri/munder-difflin). The persistent
local-service and terminal interaction design also draws on BrokkAi/mjolnir.
Their source assets have not been copied.

`internal/web/assets/atlas.json` records frame positions. The watchtower antenna
extends above its nominal lower-row cell, so its crop starts at y=480 and the
workshop crop ends at y=480. The renderer draws geometric paths and UI labels in
code, while representational buildings and actors come from the generated atlases.
