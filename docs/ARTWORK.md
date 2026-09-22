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

The field added on 2026-09-10 is drawn in `internal/web/scenery.js`: seeded grass,
dirt paths, trees, rocks, flowers, and fences, with a cached background per town.
It uses no additional image assets. Original building artwork is unchanged;
actors use tighter transparent crops, walking/tool poses, shadows, and house
activity effects. Delivery movement follows committed events and respects the
motion toggle and the operating system's reduced-motion preference.

Feature Bot received two original companion sprites on 2026-09-10:
`internal/web/assets/feature-study.png` and
`internal/web/assets/feature-reader.png`. The burgundy-roof study has an
open-book/idea-lamp sign, bookshelves, a reading lamp, and a bespectacled robot at
its desk. The matching reader wears round glasses and a burgundy waistcoat and
holds an open book. Both were generated with the built-in `image_gen` tool, and
their genuine RGBA transparency is preserved without pixel edits. They are
included with this project's Apache-2.0 distribution. Existing atlases remain
unchanged.

`docs/artwork-prompts.json` records the exact final prompts and generation mode,
plus two discarded reference-based study attempts that returned opaque painted
checkerboards. The accepted study and reader used fresh generation with textual
style direction. `internal/web/assets/atlas.json` records their source dimensions
and bounds. The seven-house layout and its delivery paths share the geometry in
`internal/web/town.js`; the feature reader appears only for committed working or
pausing state and respects reduced motion.

## Skins

The town can be viewed through more than one skin. A skin is presentation only:
`internal/web/skins.js` lists them, and each one paints the field, the houses,
a working bot and a delivery from the same committed state and the same
delivery events. The village skin in `internal/web/scenery.js` is the original
look. The choice is a header toggle (or the `S` key), is remembered per browser,
and can be requested with `?skin=` when a launcher opens the page.

The swarm defence skin added on 2026-09-22 lives in `internal/web/swarm.js` and
is drawn entirely in code: scorched ground, craters, trench roads, a stockade
around every house, gate lamps, burrows, small six-legged creatures, walkers,
a courier drone and a banner. It reuses the existing building and actor atlases
and adds no image assets. Its idea is that the bugs are literal: a delivery is
a raid that reaches a house's gate and leaves a burrow, the burrows at a gate
are that house's queue drawn from workload counts rather than from the
animation, and a working bot is a defender burning the active burrow out. Orders
from Town Hall arrive as a friendly airdrop; a shipment is a sortie leaving the
map. The look borrows only genre conventions shared by base-defence and
real-time strategy games in general (selection rings, build bars, hordes,
burrows, fog at the edges). It names no game, faction or unit, and its
creatures, palette and structures are this project's own.
