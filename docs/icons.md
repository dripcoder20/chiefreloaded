# Icons

## macOS DMG assets

Three files style the `.dmg` produced by `task darwin:package:dmg`. They are
wired up in `build/darwin/Taskfile.yml` (`create:dmg`) and **none of them is
touched by `task common:generate:icons`** — that task only regenerates
`build/darwin/icons.icns`, `build/windows/icon.ico` and
`build/darwin/Assets.car`. The DMG assets are committed artefacts that have to
be updated by hand.

| File | Role | Source |
|---|---|---|
| `build/darwin/dmg-file-icon.png` | Master raster for the `.dmg` file icon | Copy of `build/appicon-source.png` (1254×1254) |
| `build/darwin/dmg-file-icon.icns` | What `--file-icon` actually consumes | Generated from the PNG above |
| `build/darwin/dmg-background.png` | Finder window backdrop, 540×380 | Hand-authored; must stay 540×380 RGB to match `DMG_WINDOW_WIDTH`/`HEIGHT` |

The volume icon reuses `build/darwin/icons.icns`, so it needs no separate step.

### Regenerating `dmg-file-icon.icns`

`dmg-file-icon.png` is a size-exact swap — it is 1254×1254, the same dimensions
as `build/appicon-source.png`, so the master artwork is copied in without
resizing:

```bash
cp build/appicon-source.png build/darwin/dmg-file-icon.png
```

Then rebuild the `.icns`. This is the exact command used, and it is
deterministic — re-running it on the same PNG reproduces the file byte for byte:

```bash
rm -rf /tmp/dmg-file-icon.iconset && mkdir -p /tmp/dmg-file-icon.iconset
for spec in 16:icon_16x16 32:icon_16x16@2x 32:icon_32x32 64:icon_32x32@2x \
            128:icon_128x128 256:icon_128x128@2x 256:icon_256x256 \
            512:icon_256x256@2x 512:icon_512x512 1024:icon_512x512@2x; do
  px=${spec%%:*}; name=${spec#*:}
  sips -s format png -z "$px" "$px" build/darwin/dmg-file-icon.png \
    --out "/tmp/dmg-file-icon.iconset/$name.png" >/dev/null
done
iconutil -c icns /tmp/dmg-file-icon.iconset -o build/darwin/dmg-file-icon.icns
```

All ten entries are required. `iconutil` maps them onto the full variant ladder
the committed file has always carried — `ic04`/`ic05`/`ic07`/`ic08`/`ic09`/`ic10`
plus the `@2x` chunks `ic11`/`ic12`/`ic13`/`ic14` (16 → 1024 px). Dropping an
entry silently produces a sparser `.icns`, so diff the chunk table against
`git show HEAD:build/darwin/dmg-file-icon.icns` after regenerating rather than
trusting the exit code.

`iconutil -c icns` needs its `-o` argument to end in `.icns`, and the input
directory name must end in `.iconset`.

macOS-only: both `sips` and `iconutil` are macOS tools, so the `.icns` cannot be
regenerated on Linux or Windows. Because it is committed, a regeneration must be
committed too.
