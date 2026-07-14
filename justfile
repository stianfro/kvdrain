set shell := ["bash", "-euo", "pipefail", "-c"]

cache := ".cache"
bin := cache / "bin"
shellcheck_version := "0.11.0"

default:
    @just --list

tools:
    mkdir -p {{bin}}
    test -x {{bin}}/golangci-lint || GOBIN="$PWD/{{bin}}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
    test -x {{bin}}/govulncheck || GOBIN="$PWD/{{bin}}" go install golang.org/x/vuln/cmd/govulncheck@v1.1.4
    test -x {{bin}}/actionlint || GOBIN="$PWD/{{bin}}" go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
    test -x {{bin}}/yq || GOBIN="$PWD/{{bin}}" go install github.com/mikefarah/yq/v4@v4.53.3
    test -x {{bin}}/goreleaser || GOBIN="$PWD/{{bin}}" go install github.com/goreleaser/goreleaser/v2@v2.17.0

fmt:
    gofmt -w cmd internal

fmt-check:
    test -z "$(gofmt -l cmd internal)"

build:
    mkdir -p {{bin}}
    go build -trimpath -o {{bin}}/kvdrain ./cmd/kvdrain

lint: tools
    {{bin}}/golangci-lint config verify
    {{bin}}/golangci-lint run

test:
    go test ./...

test-race:
    go test -race ./...

yaml: tools
    while IFS= read -r -d '' file; do {{bin}}/yq eval '.' "$file" >/dev/null; done < <(find . -path './.git' -prune -o -type f \( -name '*.yaml' -o -name '*.yml' \) -print0)

actionlint: tools
    {{bin}}/actionlint

vuln: tools
    {{bin}}/govulncheck ./...

release-check: tools generate-release-docs
    {{bin}}/goreleaser check
    {{bin}}/goreleaser release --snapshot --clean --skip=publish,sign,sbom

tool-shellcheck:
    mkdir -p {{bin}}
    if command -v shellcheck >/dev/null 2>&1 && shellcheck --version | grep -q 'version: {{shellcheck_version}}'; then cp "$(command -v shellcheck)" {{bin}}/shellcheck; elif ! {{bin}}/shellcheck --version 2>/dev/null | grep -q 'version: {{shellcheck_version}}'; then platform=$(uname -s | tr '[:upper:]' '[:lower:]'); arch=$(uname -m); case "$arch" in arm64) arch=aarch64 ;; esac; asset=shellcheck-v{{shellcheck_version}}.$platform.$arch.tar.xz; rm -rf {{cache}}/shellcheck-v{{shellcheck_version}}; curl -fsSLO --output-dir {{cache}} "https://github.com/koalaman/shellcheck/releases/download/v{{shellcheck_version}}/$asset"; curl -fsSLo {{cache}}/sha256sums.txt "https://github.com/koalaman/shellcheck/releases/download/v{{shellcheck_version}}/sha256sums.txt"; (cd {{cache}} && if command -v sha256sum >/dev/null 2>&1; then grep "  $asset$" sha256sums.txt | sha256sum -c -; else grep "  $asset$" sha256sums.txt | shasum -a 256 -c -; fi); tar -xJf {{cache}}/$asset -C {{cache}}; cp {{cache}}/shellcheck-v{{shellcheck_version}}/shellcheck {{bin}}/shellcheck; fi
    {{bin}}/shellcheck --version | grep -q 'version: {{shellcheck_version}}'

shell-lint: tool-shellcheck
    sh -n install.sh hack/*.sh
    {{bin}}/shellcheck --exclude=SC2016,SC2329 install.sh hack/*.sh

install-test: shell-lint
    hack/test-install.sh

krew-check: tools
    hack/check-krew.sh .krew.yaml.tpl {{bin}}/yq

krew-publish:
    hack/publish-krew.sh

release-layout: release-check
    test "$(find dist -maxdepth 1 -type f -name 'kvdrain_0.0.0-SNAPSHOT-*_*_*.tar.gz' | wc -l)" -eq 4
    test "$(find dist -maxdepth 1 -type f -name 'kvdrain_0.0.0-SNAPSHOT-*_*_*.zip' | wc -l)" -eq 2
    grep -Eq '^[0-9a-f]{64}  install.sh$' dist/checksums.txt
    grep -q 'if .IsSnapshot' .goreleaser.yaml
    grep -q '.Tag' .goreleaser.yaml

compat-kubevirt:
    rm -rf {{cache}}/compat-kubevirt-1.8
    mkdir -p {{cache}}/compat-kubevirt-1.8
    cp go.mod go.sum {{cache}}/compat-kubevirt-1.8/
    cp -R cmd internal {{cache}}/compat-kubevirt-1.8/
    cd {{cache}}/compat-kubevirt-1.8 && go mod edit -replace=k8s.io/kube-openapi=k8s.io/kube-openapi@v0.0.0-20250710124328-f3f2b991d03b && go get kubevirt.io/api@v1.8.4 kubevirt.io/client-go@v1.8.4 && go mod tidy && go test ./...

generate-release-docs:
    rm -rf {{cache}}/release
    go run ./cmd/docgen {{cache}}/release

lab-smoke node kubeconfig="../lab/kubeconfig": build
    test -n {{quote(node)}}
    {{bin}}/kvdrain --kubeconfig {{quote(kubeconfig)}} --no-tty status {{quote(node)}}

lab-e2e node kubeconfig="../lab/kubeconfig": build
    hack/lab-e2e.sh {{quote(node)}} {{quote(kubeconfig)}}

ci: fmt-check lint test test-race compat-kubevirt build yaml actionlint vuln install-test krew-check release-layout
