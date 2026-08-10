# Icons

How to change Loop's app icon without reverse-engineering the Taskfiles.

## Sources and generated files

Two files are **sources** — hand-maintained, and the only things you edit to
change the icon:

| Source | What it feeds |
|---|---|
| `build/appicon.png` | 1024×1024 RGBA. The raster source for `icons.icns`, `icon.ico`, and Linux |
| `build/appicon.icon/` | Apple Icon Composer bundle (`icon.json` + `Assets/`). The source for `Assets.car` |

`build/appicon-source.png` is the 1254×1254 original the 1024×1024 `appicon.png`
was downscaled from. It is the higher-fidelity master: start from it, not from
`appicon.png`, whenever a larger raster is needed (the DMG file icon does exactly
that).

Everything below is **generated** and committed:

| Generated file | Produced by | Consumed by |
|---|---|---|
| `build/darwin/icons.icns` | `task common:generate:icons` | macOS bundle, DMG volume icon |
| `build/darwin/Assets.car` | `task common:generate:icons` (macOS host only) | macOS 26+ bundle |
| `build/windows/icon.ico` | `task common:generate:icons` | Windows `.syso` resource |

Linux generates nothing — `build/linux/nfpm/nfpm.yaml` and `create:appimage` in
`build/linux/Taskfile.yml` copy `build/appicon.png` directly.

### Changing the icon

```bash
# 1. New artwork in, at exactly 1024x1024 with alpha
sips -s format png -z 1024 1024 new-artwork.png --out build/appicon.png

# 2. Point the Icon Composer bundle at the same artwork — see the warning below
#    (drop the asset into build/appicon.icon/Assets/ and update icon.json's
#    "image-name" / "name" keys to match)

# 3. Regenerate the three derived formats
task common:generate:icons

# 4. Update the DMG assets by hand — see "macOS DMG assets" below
```

`generate:icons` uses Task's `sources:`/`generates:` fingerprinting, so a
regeneration can appear to be a no-op. Force it with
`task --force common:generate:icons` rather than editing the task definition.

### The generated files are committed to git

`build/darwin/icons.icns`, `build/darwin/Assets.car` and
`build/windows/icon.ico` are tracked, not build outputs ignored by git. **Any
regeneration has to be committed**, or the build ships the previous artwork. The
one exception is `Assets.car`'s harmless per-build churn — see
[`Assets.car` is not byte-reproducible](#assetscar-is-not-byte-reproducible).

### `Assets.car` requires a macOS host

`wails3 generate icons` only honours its `-iconcomposerinput` / `-macassetdir`
flags on macOS; on Linux or Windows it silently skips them and leaves the
committed `Assets.car` untouched. It shells out to Apple's `actool`, which does
not exist elsewhere. **Icon changes must therefore be made on a macOS host** — on
any other platform `Assets.car` keeps whatever artwork it already had, and the
run still exits 0.

## Warning: `appicon.icon/` and `appicon.png` must change together

They are two independent sources for the same icon, and **macOS 26+ prefers
`Assets.car` over `icons.icns`**. Updating `build/appicon.png` alone regenerates
the `.icns` and the `.ico` but leaves `build/appicon.icon/` pointing at the old
asset — so a macOS 26+ dock and Finder keep drawing the *old* icon while
Windows, Linux, and older macOS show the new one. That split is the single
easiest mistake to make here.

Verify the bundle really was recompiled from the asset you expect:

```bash
assetutil --info build/darwin/Assets.car
```

Look for the `appicon_Assets/<layer-name>` entry and its `RenditionName`
(`image.svg` vs a `.png`) to see which asset was compiled. Note the JSON escapes
the slash (`appicon_Assets\/…`), so grep the bare layer name.

## The Icon Composer asset is a raster-backed SVG

`build/appicon.icon/Assets/chiefloop_app_icon.svg` is **not a true vector.** It
is a single `<image href="data:image/png;base64,…">` — a 1254 px raster wrapped
in an SVG at a 1254 viewBox. `actool` rasterises it correctly, so it works today,
but it carries a raster's limits: it is no sharper than
`build/appicon-source.png`, and it will look soft rather than crisp if Apple's
icon sizes grow beyond it.

The upgrade path is genuine vector artwork. The migration is two steps: drop
`loop_icon.svg` into `build/appicon.icon/Assets/` and change `icon.json`'s
`image-name` / `name` keys to match. Nothing else in the pipeline cares.

Two `icon.json` settings are deliberately tuned for this artwork and should stay
that way for any full-colour, full-bleed replacement:

- **No `fill-specializations` on the layer.** They pin the dark and tinted
  appearances to a solid colour, which flattens colour art into a silhouette.
  They were right for the monochrome Wails mark and wrong here.
- **`position.scale` is `1.0`, not the stock `0.85`.** The artwork carries its own
  rounded-square background. At `0.85` the group's white gradient `fill` shows
  through the artwork's transparent corners as a pale rim inside the squircle.

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

## Verifying the icon in a real build

Checking the committed artwork is not enough — what ships is whatever the
packaging tasks copy into the bundle. Verify per target:

**macOS.** `task package` copies `build/darwin/icons.icns` and
`build/darwin/Assets.car` into `bin/loop.app/Contents/Resources` verbatim, so a
checksum comparison is the whole test:

```bash
shasum -a 256 bin/loop.app/Contents/Resources/{icons.icns,Assets.car}
git show HEAD:build/darwin/icons.icns | shasum -a 256
git show HEAD:build/darwin/Assets.car | shasum -a 256
```

`task run` builds `bin/loop.dev.app` the same way from the same two files, so it
carries the same icon.

To see what macOS will actually draw — rather than what is on disk — ask
LaunchServices via `NSWorkspace.icon(forFile:)` for the Finder icon and
`NSRunningApplication.icon` for the dock icon of a running instance. Both resolve
through the same caches the user sees.

**Windows.** With no Windows host, cross-build the resource object on macOS and
confirm every frame of the `.ico` is embedded byte-for-byte:

```bash
cd build && wails3 generate syso -arch amd64 -icon windows/icon.ico \
  -manifest windows/wails.exe.manifest -info windows/info.json \
  -out /tmp/wails_windows_amd64.syso
```

**Linux.** There is nothing generated to check. Both consumers copy
`build/appicon.png` directly — `build/linux/nfpm/nfpm.yaml` installs it to
`/usr/share/icons/hicolor/128x128/apps/loop.png`, and `create:appimage` in
`build/linux/Taskfile.yml` copies it next to the binary. Verifying the Linux path
means confirming `build/appicon.png` is the intended artwork and that those two
references still point at it.

### `Assets.car` is not byte-reproducible

`actool` stamps a build timestamp and a fresh UUID into every rendition filename,
so two runs of `task common:generate:icons` over identical inputs produce
`Assets.car` files with **different checksums at identical size**. A diff shows a
few hundred differing bytes in a multi-megabyte file.

This means a rebuild dirties `build/darwin/Assets.car` in `git status` even when
nothing changed. Do not commit that churn, and do not read it as a regression.
Compare the catalogs semantically instead:

```bash
assetutil --info build/darwin/Assets.car
```

The renditions, sizes, layers and appearances must match; only `Timestamp` and
the UUID embedded in each `RenditionName` may differ. `build/darwin/icons.icns`
and `build/windows/icon.ico` *are* reproducible, so they can be compared by
checksum as usual.

Because `Assets.car` is regenerated on every `task package`, checksum the bundle
against the **working tree** file it was built from, then separately confirm the
working tree is semantically equal to `HEAD`.
