#!/usr/bin/env bash
# Installs the latest k6 release binary from https://github.com/grafana/k6/releases
set -euo pipefail
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
version=$(curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/grafana/k6/releases/latest | sed 's|.*/tag/||')
curl -fsSL "https://github.com/grafana/k6/releases/download/${version}/k6-${version}-linux-${arch}.tar.gz" |
  sudo tar -xz -C /usr/local/bin --strip-components=1 "k6-${version}-linux-${arch}/k6"
k6 version
