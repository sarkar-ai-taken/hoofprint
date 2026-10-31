#!/usr/bin/env bash
# Renders docs/demo/*.txt (from hack/demo.sh) into docs/img/*.png with freeze
# (go install github.com/charmbracelet/freeze@latest) and stitches a GIF with
# ffmpeg if available.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/go/bin:$PATH"
command -v freeze >/dev/null || { echo "install freeze: go install github.com/charmbracelet/freeze@latest"; exit 1; }
mkdir -p docs/img
FONT=${FREEZE_FONT:-Menlo}
WRAP=${FREEZE_WRAP:-120}

# Colour the words that matter; freeze renders ANSI. Portable (BSD/GNU) sed.
colorize() {
  sed -E \
    -e 's#^\$ ./bin/hoofprint#$ hoofprint#' \
    -e 's/(^|[^A-Za-z_])FAIL([^A-Za-z_]|$)/\1\x1b[1;31mFAIL\x1b[0m\2/g' \
    -e 's/(^|[^A-Za-z_])PASS([^A-Za-z_]|$)/\1\x1b[1;32mPASS\x1b[0m\2/g' \
    -e 's/(^|[^A-Za-z_])UNSURE([^A-Za-z_]|$)/\1\x1b[1;33mUNSURE\x1b[0m\2/g' \
    -e 's/(^|[[:space:]])fail([[:space:]]|$)/\1\x1b[1;31mfail\x1b[0m\2/g' \
    -e 's/(^|[[:space:]])pass([[:space:]]|$)/\1\x1b[1;32mpass\x1b[0m\2/g' \
    -e 's/(^|[[:space:]])online([[:space:]]|$)/\1\x1b[32monline\x1b[0m\2/g' \
    -e 's/(^|[[:space:]])offline([[:space:]]|$)/\1\x1b[31moffline\x1b[0m\2/g' \
    -e 's/^(\$ .*)$/\x1b[1;36m\1\x1b[0m/' \
    -e 's/^(==> .*)$/\x1b[1m\1\x1b[0m/'
}

render() { # render <name>
  local src=docs/demo/$1.txt out=docs/img/$1.png
  [ -f "$src" ] || return 0
  colorize < "$src" > /tmp/hoofprint-shot.txt
  freeze /tmp/hoofprint-shot.txt --language ansi -o "$out" \
    --theme catppuccin-mocha --window --padding 24,32 --margin 0 --border.radius 10 \
    --wrap "$WRAP" --font.family "$FONT" --font.size 14 \
    --background '#1e1e2e' --shadow.blur 20 --shadow.x 0 --shadow.y 10 >/dev/null
  echo "wrote $out"
}

for f in docs/demo/*.txt; do render "$(basename "${f%.txt}")"; done

if command -v ffmpeg >/dev/null && command -v ffprobe >/dev/null; then
  # Frames must share one size: scale each to a common width, pad to the
  # tallest, then assemble. 4 seconds per frame.
  W=1200; H=0; TMP=$(mktemp -d)
  for f in docs/img/0*.png; do
    dims=$(ffprobe -v error -show_entries stream=width,height -of csv=p=0 "$f")
    w=${dims%%,*}; h=${dims##*,}
    sh=$(( h * W / w )); [ "$sh" -gt "$H" ] && H=$sh
  done
  H=$(( (H + 1) / 2 * 2 ))
  i=0
  for f in docs/img/0*.png; do
    i=$((i+1))
    ffmpeg -loglevel error -y -i "$f" -vf "scale=${W}:-2:flags=lanczos,pad=${W}:${H}:0:0:color=#1e1e2e" "$TMP/$(printf '%03d' $i).png"
  done
  rm -f docs/img/demo.gif
  ffmpeg -loglevel error -y -framerate 1/4 -i "$TMP/%03d.png" \
    -vf "split[s0][s1];[s0]palettegen=max_colors=128[p];[s1][p]paletteuse=dither=bayer" \
    -loop 0 docs/img/demo.gif && echo "wrote docs/img/demo.gif (${W}x${H}, $i frames)"
  rm -rf "$TMP"
fi
