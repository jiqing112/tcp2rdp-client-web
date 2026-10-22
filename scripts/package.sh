#!/usr/bin/env bash
# tcp2rdp-web 发布包打包规则。
#
# 用法: scripts/package.sh <goos> <goarch> <version> <outdir>
#
# 每个发布包 = tcp2rdp-web（本仓库交叉编译）+ rdp2tcp（打包时从
# https://github.com/jiqing112/tcp2rdp 最新 release 下载的对应平台二进制）
# + README.md + LICENSE。Windows 出 zip，其余平台出 tar.gz（保留可执行位）。
set -euo pipefail

os="$1"; arch="$2"; ver="$3"; out="$4"
UPSTREAM="https://github.com/jiqing112/tcp2rdp/releases/latest/download"

exe="tcp2rdp-web"; r2t="rdp2tcp"; suf=""
if [ "$os" = windows ]; then
  exe="tcp2rdp-web.exe"; r2t="rdp2tcp.exe"; suf=".exe"
fi

base="tcp2rdp-web-$ver-$os-$arch"
stage="pkg/$base"
rm -rf "$stage"
mkdir -p "$stage"

echo "==> 编译 tcp2rdp-web $ver ($os/$arch)"
CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
  go build -trimpath -ldflags "-s -w -X main.appVersion=$ver" -o "$stage/$exe" .

echo "==> 下载上游 rdp2tcp 最新版 ($os/$arch)"
curl -fsSL --retry 5 --retry-all-errors --retry-delay 2 -o "$stage/$r2t" "$UPSTREAM/rdp2tcp-$os-$arch$suf"
if [ ! -s "$stage/$r2t" ]; then
  echo "错误：rdp2tcp-$os-$arch$suf 下载为空，请检查上游 release" >&2
  exit 1
fi

cp README.md LICENSE "$stage/"
chmod +x "$stage/$exe" "$stage/$r2t"

mkdir -p "$out"
outabs=$(cd "$out" && pwd)

if [ "$os" = windows ]; then
  if command -v zip >/dev/null 2>&1; then
    ( cd "$stage" && zip -q -r "$outabs/$base.zip" . )
  elif command -v powershell.exe >/dev/null 2>&1; then
    # 本机（Windows）无 zip 命令时的兜底；CI 的 ubuntu 自带 zip，走不到这。
    # PowerShell 不认 Git Bash 的 /c/... 绝对路径，这里都用相对路径。
    powershell.exe -NoProfile -Command \
      "Compress-Archive -Path '$stage/*' -DestinationPath '$out/$base.zip' -Force"
  else
    echo "错误：打包 Windows 包需要 zip 命令" >&2
    exit 1
  fi
  echo "==> 完成: $out/$base.zip"
else
  tar -C "$stage" -czf "$outabs/$base.tar.gz" .
  echo "==> 完成: $out/$base.tar.gz"
fi
