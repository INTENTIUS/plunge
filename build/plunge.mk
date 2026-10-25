# plunge targets, included from the Makefile. Kept in their own file so upstream merges of the Makefile
# stay clean.
#
# The binary is built from pkg/cmd/pulumi (the Go package keeps upstream's path) and named plunge. Both
# versions are stamped by scripts/plunge-version.sh: plunge's own, and the Pulumi-compatible one that
# SDKs and plugins check.

PLUNGE_LDFLAGS = $(shell ./scripts/plunge-version.sh ldflags)

bin/plunge: GO_BUILD_TAGS =
bin/plunge: .make/proto .make/ensure/go $(shell bin/helpmakego pkg/cmd/pulumi)
	go build -C pkg -o ../$@ -tags="${GO_BUILD_TAGS}" -ldflags "${PLUNGE_LDFLAGS}" ${PROJECT}

.PHONY: plunge
plunge: bin/plunge

build:: bin/plunge

install:: bin/plunge
	cp $< $(PULUMI_BIN)/plunge

# Fetch the pinned dotnet and yaml language hosts for this machine into bin/, next to bin/plunge, where
# the CLI looks for bundled plugins (sdk/go/common/workspace/plugins.go, GetPluginPath). This is the
# local equivalent of what the release does through scripts/prep-for-goreleaser.sh. Java and HCL are
# not bundled: the CLI installs them on first use at the versions pinned in pkg/util/plugin.go.
.PHONY: plunge_language_hosts
plunge_language_hosts:
	./scripts/get-language-providers.sh local
	cp bin/$(shell go env GOOS)-$(subst amd64,x64,$(shell go env GOARCH))/pulumi-language-* bin/

# A local snapshot of the release archives, built with .goreleaser.plunge.yml.
.PHONY: plunge_snapshot
plunge_snapshot:
	./scripts/prep-for-goreleaser.sh local
	PULUMI_VERSION=$(shell ./scripts/plunge-version.sh pulumi) \
	PLUNGE_VERSION=$(shell ./scripts/plunge-version.sh plunge) \
		goreleaser release -f .goreleaser.plunge.yml --snapshot --clean --skip=validate
