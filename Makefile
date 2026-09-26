# 検査は、ローカルと CI で同じ版の Go とツールを使う。版がずれると、片方でだけ通る検査が生まれるため。
# CI も各ターゲットを呼ぶので、版と引数はこのファイルだけで決める。

# go.mod の go の行の版（パッチまで書く）を、手元に新しい Go があっても使う。
export GOTOOLCHAIN := go$(shell awk '$$1 == "go" { print $$2 }' go.mod)

# Go 製のツールは go run で版を指定して実行し、手元に入れた版には頼らない。
GOLANGCI_LINT := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
GOVULNCHECK   := golang.org/x/vuln/cmd/govulncheck@v1.8.0
GITLEAKS      := github.com/zricethezav/gitleaks/v8@v8.30.1
ACTIONLINT    := github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
# zizmor は Go 製ではないので、入っている版が一致するかを確かめる。
ZIZMOR_VERSION := 1.30.1

.PHONY: check fmt vet build lint test vuln secrets actionlint zizmor zizmor-version

check: fmt vet build lint test vuln secrets actionlint zizmor

# PATH の gofmt ではなく、選んだ Go に同梱の gofmt を使う。
fmt:
	@out=$$("$$(go env GOROOT)/bin/gofmt" -l .); \
	if [ -n "$$out" ]; then echo "gofmt が必要なファイル:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

# go test はテスト用のバイナリしか作らないので、実行ファイルのビルドを別に確かめる。
build:
	go build -o /dev/null ./...

lint:
	go run $(GOLANGCI_LINT) run

test:
	go test -race ./...

vuln:
	go run $(GOVULNCHECK) ./...

# 履歴のすべてのコミットと、まだコミットしていない変更の両方を検査する（I-21）。
# 追跡していない新しいファイルは、コミットした時点で履歴の検査の対象になる。
secrets:
	go run $(GITLEAKS) git --redact --no-banner .
	go run $(GITLEAKS) git --pre-commit --redact --no-banner .

actionlint:
	go run $(ACTIONLINT)

# オンラインの検査はトークンの有無で結果が変わるので、ローカルと CI を揃えるためにオフラインで実行する。
zizmor:
	@command -v zizmor >/dev/null || { echo "zizmor $(ZIZMOR_VERSION) が必要です"; exit 1; }
	@v=$$(zizmor --version | awk '{ print $$2 }'); \
	if [ "$$v" != "$(ZIZMOR_VERSION)" ]; then echo "zizmor の版が違います: $$v（必要: $(ZIZMOR_VERSION)）"; exit 1; fi
	zizmor --offline .github/workflows

zizmor-version:
	@echo $(ZIZMOR_VERSION)
