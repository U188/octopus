#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <os> <arch> <destination directory>" >&2
    exit 2
fi

version="1.14.1"
os="$1"
arch="$2"
destination="$3"

case "${os}/${arch}" in
    linux/x86_64) platform="linux-amd64"; checksum="12cb2816b52febb356f6a885b740cc8758c3f30b8ae0ca8edba80f0d2d35343f" ;;
    linux/arm64) platform="linux-arm64"; checksum="6060b42fa84c5dcaeae1799af7f61b0f1ae4855d9d5ddc9e02baba17154b3ae2" ;;
    linux/armv7) platform="linux-armv7"; checksum="f2c8af2e3576f40f8ab0d06e1d44840e4eb6bcf410ba8d42381781cb0d6fe41b" ;;
    linux/x86) platform="linux-386"; checksum="b3126212b32e5b222ae79118618ecffa7d4a1cc56d781973a8af81841359ba02" ;;
    linux-musl/x86_64) platform="linux-amd64-musl"; checksum="b907365b154e4a7e3e40be15c2cd83433c0fa65c7dc736bdb1b5face2afe4501" ;;
    linux-musl/arm64) platform="linux-arm64-musl"; checksum="d94fc9704372ca2fa2854e54c20b406e4b8779b5ccdd0c557da90ea9344e9631" ;;
    linux-musl/armv7) platform="linux-armv7-musl"; checksum="4004839c33cd5fb4fcb0b771bd37b59661973c1635c674bd6c46a59a7415d4d5" ;;
    linux-musl/x86) platform="linux-386-musl"; checksum="5d56bb3c66b1a7e4d1e04710d660531de7c594bb61be2c142bf3ad4c5a238c2c" ;;
    darwin/x86_64) platform="darwin-amd64"; checksum="b34381b047106fe84895df14f7aaae06f3182130b728006944deb0d59d8590c3" ;;
    darwin/arm64) platform="darwin-arm64"; checksum="b9024642ef7b4848252df5469b7f60ef3c18bb5e217a16a0934f0174f8ad11b4" ;;
    windows/x86_64) platform="windows-amd64"; checksum="5197f16d492d93202dc623622149a6ed040f8eca263128f91d603f2b901baa89" ;;
    windows/x86) platform="windows-386"; checksum="cad9ff678c671374c316f3c1094b9457960c84cb20bf35d528380f79a1e6d643" ;;
    android/x86_64) platform="android-amd64"; checksum="43d1b49a3086ad12092f028cbe3efb28422c2a6114d0dfb0ea18d0c2427573dc" ;;
    android/arm64) platform="android-arm64"; checksum="34e2373cfcdd17ef3a0cac13d7f9f971e206257413a9f7b1da63ea02bcf88aab" ;;
    android/armv7) platform="android-arm"; checksum="3484170da8102ef4225ac38da29586068c70d1520158d18416d150f92873ba2a" ;;
    android/x86) platform="android-386"; checksum="6f1b22d97198c0e09a015b94622d981f71d93a43edc4fc2b821000e759523a7d" ;;
    *) echo "Unsupported sing-box target: ${os}/${arch}" >&2; exit 2 ;;
esac

if [ "$os" = "windows" ]; then
    extension="zip"
    binary="sing-box.exe"
else
    extension="tar.gz"
    binary="sing-box"
fi

mkdir -p "$destination"
temporary="$(mktemp -d)"
trap 'rm -r -- "$temporary"' EXIT
archive="${temporary}/sing-box.${extension}"
url="https://github.com/SagerNet/sing-box/releases/download/v${version}/sing-box-${version}-${platform}.${extension}"
curl --fail --location --retry 3 --silent --show-error --output "$archive" "$url"
if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$archive")"
else
    actual="$(shasum -a 256 "$archive")"
fi
if [ "${actual%% *}" != "$checksum" ]; then
    echo "sing-box ${platform} SHA-256 mismatch" >&2
    exit 1
fi

entry="sing-box-${version}-${platform}/${binary}"
license_entry="sing-box-${version}-${platform}/LICENSE"
if [ "$os" = "windows" ]; then
    unzip -p "$archive" "$entry" > "${temporary}/${binary}"
    unzip -p "$archive" "$license_entry" > "${temporary}/LICENSE"
else
    tar -xOzf "$archive" "$entry" > "${temporary}/${binary}"
    tar -xOzf "$archive" "$license_entry" > "${temporary}/LICENSE"
fi
test -s "${temporary}/${binary}"
test -s "${temporary}/LICENSE"
chmod 755 "${temporary}/${binary}"
chmod 644 "${temporary}/LICENSE"
mv -f "${temporary}/${binary}" "${destination}/${binary}"
mv -f "${temporary}/LICENSE" "${destination}/LICENSE"
