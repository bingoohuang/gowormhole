.PHONY: wasm

wasm: js
	GOOS=js GOARCH=wasm go build -o ./web/webwormhole.wasm ./web
	cp $(shell go env GOROOT)/misc/wasm/wasm_exec.js ./web/wasm_exec.js

.PHONY: webwormhole-ext.zip
webwormhole-ext.zip: wasm
	zip -j webwormhole-ext.zip ./web/* -x '*.git*' '*.go' '*Dockerfile'

.PHONY: webwormhole-src.zip
webwormhole-src.zip:
	zip -r -FS webwormhole-src.zip  * -x '*.git*' webwormhole-src.zip webwormhole-ext.zip

.PHONY: all
all: webwormhole-ext.zip

.PHONY: fmt
fmt:
	prettier -w --use-tabs web/*.ts
	go fmt ./...

.PHONY: js
js:
	tsc -T ES2018 --strict web/main.ts
	tsc -T ES2018 --strict web/ww.ts
	tsc -T ES2018 --strict web/sw.ts

UNAME := $(shell uname)

ifeq ($(UNAME), Darwin)
    DYLIB_EXT := .dylib
    FLAGS := -ldflags -s
else
    DYLIB_EXT := .so
    FLAGS := ''
endif

so:
	# New in Go 1.5, build Go dynamic lib
	go build $(FLAGS) -o gowormhole$(DYLIB_EXT) -buildmode=c-shared ./cmd/gowormhole
dll:
	git clone https://github.com/bingoohuang/gowormhole.git
	cd gowormhole
	$env:GOPROXY = "https://goproxy.cn"
	go build -ldflags -s -o gowormhole.dll -buildmode=c-shared ./cmd/gowormhole

clean:
	rm -f awesome.*
