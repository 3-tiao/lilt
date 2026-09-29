#!/bin/sh
# Install a published, notarized macOS arm64 release without modifying shell config.
set -eu

fail() { printf 'lilt install: %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] || fail 'requires macOS arm64; see docs/getting-started/install.md for other platforms'
major=$(sw_vers -productVersion | cut -d. -f1)
case "$major" in ''|*[!0-9]*) fail 'cannot determine macOS version' ;; esac
[ "$major" -ge 14 ] || fail 'requires macOS 14 or later'
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v shasum >/dev/null 2>&1 || fail 'shasum is required'

repo=https://github.com/3-tiao/lilt
if [ -n "${LILT_VERSION:-}" ]; then
    version=${LILT_VERSION#v}
else
    tag=$(curl -fsSL https://api.github.com/repos/3-tiao/lilt/releases/latest |
        sed -n 's/^.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
    version=${tag#v}
fi
printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || fail 'no public stable release found; set LILT_VERSION=X.Y.Z to select one'
tag=v$version

base=${HOME:?HOME is required}/.local/share/lilt
bin=$HOME/.local/bin
target=$base/$tag
mkdir -p "$base" "$bin"
[ ! -e "$target" ] && [ ! -L "$target" ] || fail "$tag is already installed at $target"
if [ -e "$bin/lilt" ] || [ -L "$bin/lilt" ]; then
    [ -L "$bin/lilt" ] || fail "$bin/lilt already exists and is not managed by this installer"
    case "$(readlink "$bin/lilt")" in
        "$base"/v*/launcher) ;;
        *) fail "$bin/lilt is not managed by this installer" ;;
    esac
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/lilt-install.XXXXXX") || fail 'cannot create download directory'
stage=
link=
pending_target=
cleanup() {
    rm -rf "$tmp"
    [ -z "$stage" ] || rm -rf "$stage"
    [ -z "$link" ] || rm -f "$link"
    [ -z "$pending_target" ] || rm -rf "$pending_target"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM
asset=lilt-$tag-darwin-arm64.tar.gz
url=$repo/releases/download/$tag
curl -fL --retry 3 -o "$tmp/$asset" "$url/$asset" || fail "cannot download $url/$asset (the Release may still be private)"
curl -fL --retry 3 -o "$tmp/$asset.sha256" "$url/$asset.sha256" || fail 'cannot download checksum'
expected=$(sed -n '1s/[[:space:]].*$//p' "$tmp/$asset.sha256")
printf '%s\n' "$expected" | grep -Eq '^[0-9a-fA-F]{64}$' || fail 'invalid SHA-256 checksum file'
actual=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
[ "$actual" = "$expected" ] || fail 'SHA-256 mismatch; nothing was installed'

# Only the four top-level release entries may be extracted. Reject traversal
# before tar sees any entry; the signed release is still the trust boundary.
tar -tzf "$tmp/$asset" | awk '
    /^\// { bad=1 }
    { n=split($0, p, "/"); for (i=1; i<=n; i++) if (p[i]=="..") bad=1 }
    !($0=="lilt" || $0 ~ /^lilt-player\.app\// || $0 ~ /^lilt-audio\.app\// || $0 ~ /^skills\//) { bad=1 }
    END { exit bad }
' || fail 'unexpected archive contents'
stage=$(mktemp -d "$base/.install.XXXXXX") || fail 'cannot stage installation'
tar -xzf "$tmp/$asset" -C "$stage" || fail 'cannot extract release'
[ -f "$stage/lilt" ] && [ -x "$stage/lilt" ] &&
    [ -f "$stage/lilt-player.app/Contents/MacOS/lilt-player" ] &&
    [ -f "$stage/lilt-audio.app/Contents/MacOS/lilt-audio" ] || fail 'release is missing the CLI or signed helpers'
for app in "$stage/lilt-player.app" "$stage/lilt-audio.app"; do
    codesign --verify --strict "$app" >/dev/null 2>&1 || fail 'release helper signature is invalid'
    spctl -a --type execute "$app" >/dev/null 2>&1 || fail 'Gatekeeper rejected a release helper'
done

cat > "$stage/launcher" <<'LAUNCHER'
#!/bin/sh
set -eu
path=$0
if [ -L "$path" ]; then
    dest=$(readlink "$path")
    case "$dest" in /*) path=$dest ;; *) path=$(dirname "$path")/$dest ;; esac
fi
root=$(CDPATH= cd -- "$(dirname -- "$path")" && pwd)
export LILT_PLAYER_PATH="$root/lilt-player.app"
export LILT_AUDIO_PATH="$root/lilt-audio.app"
exec "$root/lilt" "$@"
LAUNCHER
chmod 755 "$stage/launcher"
mv "$stage" "$target"
stage=
pending_target=$target
link=$bin/.lilt-link-$$
ln -s "$target/launcher" "$link"
mv -f "$link" "$bin/lilt"
link=
pending_target=
printf 'Installed lilt %s at %s\n' "$version" "$bin/lilt"
case ":$PATH:" in
    *":$bin:"*) ;;
    *) printf 'Add %s to PATH to run lilt from any directory.\n' "$bin" ;;
esac
