#!/usr/bin/env bash
# Usage: fetch-ffmpeg.sh linux|windows DEST
# Downloads the pinned ffmpeg build (see ffmpeg.env), checks it and puts
# ffmpeg, ffprobe, their libraries (Windows) and LICENSE.txt in DEST.
set -euo pipefail
here=$(dirname "$0")
source "$here/ffmpeg.env"
os=$1 dest=$2
base="https://github.com/BtbN/FFmpeg-Builds/releases/download/$BTBN_RELEASE"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if [[ $os == windows ]]; then
  name="ffmpeg-$FFMPEG_VERSION-win64-gpl-shared-$FFMPEG_BRANCH"
  file="$name.zip" sum=$WIN64_SHA256
else
  name="ffmpeg-$FFMPEG_VERSION-linux64-gpl-$FFMPEG_BRANCH"
  file="$name.tar.xz" sum=$LINUX64_SHA256
fi
curl -fsSL --retry 3 -o "$tmp/$file" "$base/$file"
echo "$sum  $tmp/$file" | sha256sum -c --quiet
if [[ $os == windows ]]; then
  (cd "$tmp" && 7z x -y -bso0 "$file")
else
  tar -xJf "$tmp/$file" -C "$tmp"
fi
mkdir -p "$dest"
src="$tmp/$name"
cp "$src/LICENSE.txt" "$dest/"
if [[ $os == windows ]]; then
  cp "$src"/bin/ffmpeg.exe "$src"/bin/ffprobe.exe "$src"/bin/*.dll "$dest/"
else
  cp "$src"/bin/ffmpeg "$src"/bin/ffprobe "$dest/"
fi
