#!/bin/sh
# Render the PNGs and the macOS .icns from the SVGs that generate.py writes.
# macOS only (iconutil, sips); needs rsvg-convert (brew install librsvg).
#
#   python3 scripts/logo/generate.py && scripts/logo/render.sh
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
brand="$root/assets/brand"
icons="$root/tauri-wrapper/src-tauri/icons"
srgb="/System/Library/ColorSync/Profiles/sRGB Profile.icc"

# Tagging sRGB keeps macOS from reading the colours as generic RGB, which mutes them.
render() { # svg size out
	rsvg-convert -w "$2" -h "$2" "$1" -o "$3"
	sips --embedProfileIfNone "$srgb" "$3" >/dev/null
}

for svg in "$brand"/logo-*.svg; do
	name=$(basename "$svg" .svg)
	# The desktop Dock icon (app_icon.rs) uses Apple's grid, not the full-bleed tile.
	render "$brand/app-$name.svg" 1024 "$brand/$name.png"
	render "$brand/touch-$name.svg" 180 "$brand/touch-$name.png"
done
render "$root/assets/logo.svg" 512 "$root/assets/logo.png"
render "$brand/touch-logo-raven.svg" 180 "$root/assets/apple-touch-icon.png"
render "$root/assets/logo.svg" 1024 "$icons/icon.png"

# Each size from the vector, rather than downscaling one bitmap, keeps small sizes sharp.
set_dir=$(mktemp -d)/icon.iconset
mkdir -p "$set_dir"
for size in 16 32 128 256 512; do
	render "$brand/app-logo-raven.svg" "$size" "$set_dir/icon_${size}x${size}.png"
	render "$brand/app-logo-raven.svg" $((size * 2)) "$set_dir/icon_${size}x${size}@2x.png"
done
iconutil -c icns "$set_dir" -o "$icons/icon.icns"
rm -r "$(dirname "$set_dir")"
