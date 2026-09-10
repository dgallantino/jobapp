BUILDDIR ?= build
BINARY   := $(BUILDDIR)/jobapp

UNIT_IN := \
	systemd/jobapp.service.in \
	systemd/jobapp-crawl.service.in \
	systemd/jobapp-telegram.service.in

UNIT_COPY := \
	systemd/jobapp.socket \
	systemd/jobapp-crawl.timer \
	systemd/jobapp-telegram.timer

# Main binary inputs (exclude scripts/).
GO_SRCS := $(shell find cmd internal -type f \( \
	-name '*.go' -o -name '*.sql' -o -name '*.html' -o -name '*.js' \
	\) 2>/dev/null)

.PHONY: all build install enable disable uninstall clean

ifeq ($(wildcard .install-paths),)
_paths_ok := $(shell ./scripts/configure.sh --paths-only >&2 && echo yes)
ifeq ($(_paths_ok),)
$(error failed to generate .install-paths)
endif
endif
include .install-paths

.install-paths:
	./scripts/configure.sh --paths-only

UNIT_GEN := $(addprefix $(JOBAPP_UNITDIR)/,$(basename $(notdir $(UNIT_IN))))
UNIT_COPIED := $(addprefix $(JOBAPP_UNITDIR)/,$(notdir $(UNIT_COPY)))
UNIT_DST := $(UNIT_GEN) $(UNIT_COPIED)
UNIT_STAMP := $(JOBAPP_UNITDIR)/.jobapp-units.stamp

INSTALL_TARGETS := $(JOBAPP_GOBIN)/jobapp $(JOBAPP_ENV) $(UNIT_STAMP)
ifneq ($(JOBAPP_SYMLINK),0)
INSTALL_TARGETS += $(JOBAPP_USERBIN)/jobapp
endif

all: build

build: $(BINARY)

$(BINARY): $(GO_SRCS) go.mod go.sum
	mkdir -p $(BUILDDIR)
	go build -o $@ ./cmd/jobapp

# Prefer: ./scripts/configure.sh && make build && make install
install: $(INSTALL_TARGETS)
	mkdir -p "$(JOBAPP_DATADIR)"

ifeq ($(wildcard .env),)
.env:
	@echo "missing .env — run ./scripts/configure.sh first" >&2
	@false
endif

$(JOBAPP_GOBIN)/jobapp: $(BINARY)
	install -d "$(JOBAPP_GOBIN)"
	install -m 755 $< $@

$(JOBAPP_USERBIN)/jobapp: $(JOBAPP_GOBIN)/jobapp
	install -d "$(JOBAPP_USERBIN)"
	ln -sfn "$(JOBAPP_GOBIN)/jobapp" $@

$(JOBAPP_ENV): .env
	install -d "$(JOBAPP_CONFIGDIR)"
	install -m 600 $< $@

$(JOBAPP_UNITDIR)/%.service: systemd/%.service.in
	install -d "$(JOBAPP_UNITDIR)"
	sed \
		-e 's|@JOBAPP_BIN@|$(JOBAPP_BIN)|g' \
		-e 's|@JOBAPP_DATADIR@|$(JOBAPP_DATADIR)|g' \
		-e 's|@JOBAPP_ENV@|$(JOBAPP_ENV)|g' \
		$< > $@

$(JOBAPP_UNITDIR)/%: systemd/%
	install -d "$(JOBAPP_UNITDIR)"
	install -m 644 $< $@

$(UNIT_STAMP): $(UNIT_DST)
	systemctl --user daemon-reload
	touch $@

enable:
	systemctl --user enable --now jobapp.socket
	systemctl --user enable --now jobapp-crawl.timer
	systemctl --user enable --now jobapp-telegram.timer
	@echo "Timers and the socket stop after logout unless lingering is on:"
	@echo "  loginctl enable-linger \$$USER"

disable:
	systemctl --user disable --now jobapp.socket jobapp-crawl.timer jobapp-telegram.timer || true

uninstall: disable
	rm -f "$(JOBAPP_GOBIN)/jobapp"
	if [ -L "$(JOBAPP_USERBIN)/jobapp" ]; then rm -f "$(JOBAPP_USERBIN)/jobapp"; fi
	rm -f $(UNIT_DST) $(UNIT_STAMP)
	systemctl --user daemon-reload
	# leaves $(JOBAPP_ENV) and $(JOBAPP_DB) in place

clean:
	rm -rf $(BUILDDIR)
