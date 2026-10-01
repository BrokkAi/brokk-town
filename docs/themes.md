# Themes and artwork

The browser board has two skins. Both read the same committed snapshot, draw the
same committed events, and hand every command back to the board unchanged. A
theme changes what the town looks like, never what it does.

## Town

The default village view. Each house is a building, each worker walks a road
between them, and each delivery is drawn as it happens. The scenery
(`internal/web/scenery.js`) is painted once per town from a seed derived from the
town ID, so a town looks the same across reloads and machines.

## Frontline

The alternate skin (`internal/web/frontline.js`, `skins.js`). Each repository is
a base flying one of three armies, and each committed delivery is a strike
between two installations:

| Faction | Species | Feel |
| --- | --- | --- |
| Vanguard | Humans | Steel-blue armour, disciplined. |
| Ascendancy | Humanoid aliens | Violet, patient, already victorious. |
| Hive | Swarm of bugs | Chitin and amber, many bodies one mind. |

Frontline is explicitly "a look, not a lever": the code only reads the snapshot
and paints it. A strike is one delivery Town already committed — the same task
moving between the same two houses — so the theme cannot move work by itself.

The chosen skin and the faction picked for each base are stored in that browser's
local storage. They are not town state, are not sent anywhere, and do not affect
scheduling. Reduced-motion settings stop the animation; the state underneath is
unchanged.

## Assets

Everything the board draws ships in `internal/web/assets/`:

| File | Use |
| --- | --- |
| `buildings-atlas.png`, `actors-atlas.png`, `atlas.json` | Town buildings and workers, with source rectangles described in the atlas. |
| `frontline-vanguard.png`, `frontline-ascendancy.png`, `frontline-hive.png` | Frontline bases for the three factions. |
| `frontline-craft.png`, `frontline-defenders.png` | Frontline strike craft and defenders. |
| `feature-reader.png`, `feature-study.png` | Feature Bot characters. |

`atlas.json` records the grid, dimensions and named source rectangles, including
the note that the watchtower antenna crosses the nominal 512-pixel grid line, so
the cell must be drawn from the recorded rectangle rather than a naive grid
slice.

The original PNGs are preserved without image edits and alpha channels are kept.

## Provenance

The character and environment artwork is original, generated for this project,
and distributed under this project's Apache-2.0 license. No external game sprites
or third-party art are bundled. Where a generation prompt was recorded it lives
next to its asset — for example
`bots/feature-bot/docs/artwork-prompt.json` for the Feature Bot reader.

If you add art:

1. Keep the original RGBA PNG, unedited.
2. Record the source and, when it was generated, the exact prompt and tool mode
   beside the asset.
3. Confirm the license is compatible with Apache-2.0 and note attribution in
   [NOTICE](../NOTICE) when required.
4. Update `atlas.json` if the asset is drawn from a sheet.
