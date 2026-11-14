# Makefile for btoon-go (pure Go; no C toolchain needed except for -race).

GO       ?= go
FUZZTIME ?= 60s
# Location of a btoon-core checkout, for sync-vectors.
CORE     ?= ../btoon-core
VECTORS  := $(CORE)/tests/vectors/btoon-vectors.json

.PHONY: all check test test-short vet fmt fmt-check fuzz bench example sync-vectors help

all: check

## check: everything CI runs (formatting, vet, race tests)
check: fmt-check vet test

## test: run all tests, including the conformance vectors, with the race detector
test:
	$(GO) test -race -count=1 ./...

## test-short: run all tests without the race detector
test-short:
	$(GO) test -count=1 ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## fmt: format the code in place
fmt:
	gofmt -s -w .

## fmt-check: fail if any file needs gofmt
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "files need gofmt:"; echo "$$out"; exit 1; fi

## fuzz: fuzz the decoder and the LZ4 block decoder (FUZZTIME=60s each by default)
fuzz:
	$(GO) test -run='^$$' -fuzz=FuzzDecode -fuzztime=$(FUZZTIME) .
	$(GO) test -run='^$$' -fuzz=FuzzLZ4Block -fuzztime=$(FUZZTIME) ./codecs
	$(GO) test -run='^$$' -fuzz=FuzzLZ4RoundTrip -fuzztime=$(FUZZTIME) ./codecs

## bench: run the benchmarks
bench:
	$(GO) test -run='^$$' -bench=. -benchmem .

## example: run examples/basic
example:
	$(GO) run ./examples/basic

## sync-vectors: copy the conformance vectors from btoon-core (CORE=../btoon-core)
sync-vectors:
	@test -f "$(VECTORS)" || { echo "not found: $(VECTORS) (set CORE=path/to/btoon-core)"; exit 1; }
	cp "$(VECTORS)" testdata/btoon-vectors.json
	@echo "testdata/btoon-vectors.json updated from $(VECTORS)"

## help: list the targets
help:
	@sed -n 's/^## //p' $(MAKEFILE_LIST)
