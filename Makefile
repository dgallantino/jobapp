PREFIX   ?= /opt/jobapp
BUILDDIR ?= build
UNITDIR  ?= /etc/systemd/system
BINARY   := $(BUILDDIR)/jobapp

UNIT_SRCS := \
	systemd/jobapp.socket \
	systemd/jobapp.service \
	systemd/jobapp-crawl.service \
	systemd/jobapp-crawl.timer \
	systemd/jobapp-telegram.service \
	systemd/jobapp-telegram.timer

UNIT_DST := $(addprefix $(UNITDIR)/,$(notdir $(UNIT_SRCS)))
UNIT_STAMP := $(UNITDIR)/.jobapp-units.stamp

# Main binary inputs (exclude scripts/).
GO_SRCS := $(shell find cmd internal -type f \( \
	-name '*.go' -o -name '*.sql' -o -name '*.html' -o -name '*.js' \
	\) 2>/dev/null)

.PHONY: all build install enable disable uninstall clean

all: build

build: $(BINARY)

$(BINARY): $(GO_SRCS) go.mod go.sum
	mkdir -p $(BUILDDIR)
	go build -o $@ ./cmd/jobapp

# Only rebuilds / reinstalls what is out of date (timestamp-based).
# Prefer: ./scripts/configure.sh && make build && sudo make install
install: $(PREFIX)/jobapp $(PREFIX)/.env $(UNIT_STAMP)

ifeq ($(wildcard .env),)
.env:
	@echo "missing .env — run ./scripts/configure.sh first" >&2
	@false
endif

$(PREFIX)/jobapp: $(BINARY)
	install -d $(PREFIX)
	install -m 755 $< $@

$(PREFIX)/.env: .env
	install -d $(PREFIX)
	install -m 600 $< $@

$(UNITDIR)/%: systemd/%
	install -m 644 $< $@

$(UNIT_STAMP): $(UNIT_DST)
	systemctl daemon-reload
	touch $@

enable:
	systemctl enable --now jobapp.socket
	systemctl enable --now jobapp-crawl.timer
	systemctl enable --now jobapp-telegram.timer

disable:
	systemctl disable --now jobapp.socket jobapp-crawl.timer jobapp-telegram.timer || true

uninstall: disable
	rm -f $(PREFIX)/jobapp
	rm -f $(UNIT_DST) $(UNIT_STAMP)
	systemctl daemon-reload
	# leaves $(PREFIX)/.env and jobs.db in place

clean:
	rm -rf $(BUILDDIR)
