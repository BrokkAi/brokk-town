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

## Frontline skin (2026-09-22)

The browser ships a second theme beside the town. Frontline draws the same
state as a base assault: each repository is a base flying one of three armies
— humans (Vanguard), humanoid aliens (Ascendancy) and a swarm (Hive) — and
every committed delivery is drawn as a strike run between installations.

`internal/web/frontline.js` paints it with the canvas primitives, in the same
style as the field in `scenery.js`: ashen ground, craters, armored trackways
over the shared road geometry, hazard-striped base plates, a structure per
role, patrolling garrisons, strike craft carrying the real issue or pull
request number, and an impact burst where the work arrived. No new image assets
are used, so the atlases and `docs/artwork-prompts.json` are unchanged.
`internal/web/skins.js` is the one surface both themes answer.

## Frontline artwork refresh (2026-09-23)

Frontline now uses four original RGBA atlases in `internal/web/assets`:
`frontline-vanguard.png`, `frontline-ascendancy.png`, `frontline-hive.png`, and
`frontline-craft.png`. The three building atlases each hold eight structures in
role order, four columns by two rows. The craft atlas holds one craft per
faction, left to right. They were generated with the built-in `image_gen` tool
for this project and are included under its Apache-2.0 distribution. The art
uses the detailed isometric industrial, psionic, and biological vocabulary of
classic space strategy games; it does not reuse game assets or named designs.

`frontline.js` draws the atlases over the existing seeded battlefield and
retains its canvas shapes as a loading fallback. The browser loads images once
per faction, outside the draw loop. Committed deliveries, animation timing,
reduced motion, and all service commands are unchanged. The Frontline CSS adds
metal-framed map and HUD surfaces. The Town skin still uses its original art.

The theme is presentation only. A base's faction comes from the repository name
and can be pinned per base in the browser; `?skin=frontline` opens the theme for
whoever the link is sent to. Strikes still follow committed events, the motion
toggle and the operating system's reduced-motion setting, and the theme never
writes to GitHub or changes what a delivery means.

## Frontline attacks and defenders (2026-09-23)

`frontline-defenders.png` is a new original transparent 3-column by 2-row
atlas generated with the built-in `image_gen` tool. The top row contains one
ground defender for each faction (armored human, crystalline alien, chitinous
swarm); the lower row contains matching defensive emplacements. The final
prompt asked for six separate isometric game sprites, each centered in its
cell, with genuine transparency and no text, logos, or copied game units.

Every Frontline installation now holds two ground units and one emplacement.
They stand still when their worker is idle, patrol lightly when it is working,
and respond only when a committed delivery approaches. The delivery craft
fires faction-specific volleys: human tracers and smoke, alien energy lances,
or arcing swarm spores. Its arrival produces a matching explosion, energy
burst, or acid splash. These effects derive from the existing event, route,
faction, and frame time; they do not create commands or events. Image requests
start when the Frontline skin is selected, outside the canvas draw loop.
