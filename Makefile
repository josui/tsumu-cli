# tsumu CLI — 构建和安装
# 使用方法：
#   make build    只编译，不安装
#   make install  编译 + 安装到 /usr/local/bin（tsumu 和 tm 都能用）
#   make clean    删除编译产物

PREFIX ?= /usr/local/bin
VERSION := $(shell grep 'const Version' cmd/version.go | sed 's/.*"\(.*\)"/\1/')

build:
	go build -o tsumu .

install: build
	cp tsumu $(PREFIX)/tsumu
	ln -sf $(PREFIX)/tsumu $(PREFIX)/tm

release:
	@if git rev-parse "v$(VERSION)" >/dev/null 2>&1; then \
		echo "Tag v$(VERSION) already exists. Update cmd/version.go first."; \
		exit 1; \
	fi
	git tag -a "v$(VERSION)" -m "v$(VERSION)"
	@echo "Created tag v$(VERSION). Run 'git push origin v$(VERSION)' to publish."

clean:
	rm -f tsumu

.PHONY: build install release clean
