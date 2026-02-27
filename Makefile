# tsumu CLI — 构建和安装
# 使用方法：
#   make build    只编译，不安装
#   make install  编译 + 安装到 /usr/local/bin（tsumu 和 tm 都能用）
#   make clean    删除编译产物

PREFIX ?= /usr/local/bin

build:
	go build -o tsumu .

install: build
	cp tsumu $(PREFIX)/tsumu
	ln -sf $(PREFIX)/tsumu $(PREFIX)/tm

clean:
	rm -f tsumu

.PHONY: build install clean
