TAILWIND_VERSION := v4.3.4

export PATH := $(CURDIR)/.bin:$(PATH)

-include .env
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env 2>/dev/null)
export PRATA_DB_PATH ?= $(CURDIR)/.tmp/prata.db

all: help

.PHONY: help
help: Makefile
	@echo
	@echo "Choose a make command to run"
	@echo
	@sed -n 's/^##//p' $< | column -t -s ':' |  sed -e 's/^/ /'
	@echo


## setup: install air and the tailwind standalone cli into .bin
.PHONY: setup
setup:
	@GOBIN=$(CURDIR)/.bin go install github.com/air-verse/air@latest
	@curl -sSfL -o .bin/tailwindcss https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-linux-x64
	@chmod +x .bin/tailwindcss


## start/server: hot reload the server, regenerating templ and css
.PHONY: start/server
start/server:
	air \
	--tmp_dir ".tmp" \
	--build.cmd "go generate ./server/internal/web && go build -o .tmp/server ./server/cmd/server" \
	--build.entrypoint ".tmp/server" \
	--build.include_dir "server,shared" \
	--build.include_ext "go,templ,css" \
	--build.exclude_regex "_templ.go,_test.go" \
	--build.exclude_file "server/internal/web/static/app.css" \
	--build.send_interrupt "true" \
	--build.kill_delay "1s" \
	--build.stop_on_error "false" \
	--misc.clean_on_exit false


## start/tui: hot reload the tui
.PHONY: start/tui
start/tui:
	air \
	--tmp_dir ".tmp" \
	--build.cmd "go build -o .tmp/tui ./tui/cmd/tui" \
	--build.entrypoint ".tmp/tui" \
	--build.include_dir "tui,shared" \
	--build.include_ext "go" \
	--build.exclude_regex "_test.go" \
	--build.send_interrupt "true" \
	--build.kill_delay "1s" \
	--build.stop_on_error "false" \
	--misc.clean_on_exit false


## build: build server and tui into bin
.PHONY: build
build: build/server build/tui

## build/server: build the server into bin/server
.PHONY: build/server
build/server:
	go build -o bin/server ./server/cmd/server

## build/tui: build the tui into bin/tui
.PHONY: build/tui
build/tui:
	go build -o bin/tui ./tui/cmd/tui


## test: test server, tui and shared
.PHONY: test
test:
	go test ./server/... ./tui/... ./shared/...

## test/server: test the server
.PHONY: test/server
test/server:
	go test ./server/...

## test/tui: test the tui
.PHONY: test/tui
test/tui:
	go test ./tui/...
