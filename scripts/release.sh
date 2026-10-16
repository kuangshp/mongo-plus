#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
mode="${1:-check}"
if [[ "$mode" != check && "$mode" != publish ]]; then
  echo 'Usage: bash scripts/release.sh check|publish' >&2
  exit 2
fi
version="$(sed -nE 's/^const Version = "(v[0-9]+\.[0-9]+\.[0-9]+)"$/\1/p' version.go)"
if [[ -z "$version" ]] || ! grep -Fxq "## $version" CHANGELOG.md; then
  echo 'version.go 必须包含有效版本，CHANGELOG.md 必须包含对应发布记录。' >&2
  exit 1
fi
if git show-ref --verify --quiet "refs/tags/$version"; then
  echo "本地标签 $version 已存在，拒绝覆盖。" >&2
  exit 1
fi
remote_tag="$(git ls-remote --tags origin "refs/tags/$version")"
if [[ -n "$remote_tag" ]]; then
  echo "远程标签 $version 已存在，拒绝覆盖。" >&2
  exit 1
fi
if [[ -z "${MONGOPLUS_TEST_URI:-}" ]]; then
  echo '发布检查必须设置测试专用 MONGOPLUS_TEST_URI，不能跳过真实数据库集成。' >&2
  exit 1
fi
command -v goctl >/dev/null
export MONGOPLUS_GIN_INTEGRATION=1
export MONGOPLUS_GOCTL_INTEGRATION=1

if [[ "$mode" == publish ]]; then
  if [[ -n "$(git status --porcelain)" ]]; then
    echo '请先提交全部修复；发布必须使用干净的工作区。' >&2
    exit 1
  fi
  branch="$(git symbolic-ref --quiet --short HEAD)"
  revision="$(git rev-parse HEAD)"
fi

git diff --check
go vet ./...
go test -race -count=1 -timeout=15m ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

if [[ "$mode" == check ]]; then
  echo "$version 发布检查通过，未创建标签或推送。"
  exit 0
fi
if [[ "$revision" != "$(git rev-parse HEAD)" ]] || [[ -n "$(git status --porcelain)" ]]; then
  echo '检查期间工作区或提交发生变化，拒绝发布。' >&2
  exit 1
fi
git tag -a "$version" "$revision" -m "release $version"
git push --atomic origin "HEAD:refs/heads/$branch" "refs/tags/$version"
