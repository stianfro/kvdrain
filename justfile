set shell := ["bash", "-euo", "pipefail", "-c"]

cache := ".cache"
bin := cache / "bin"

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

ci: fmt-check lint test test-race compat-kubevirt build yaml actionlint vuln release-check
