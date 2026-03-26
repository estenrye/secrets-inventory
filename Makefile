.PHONY: help tidy build clean

BINARY_NAME ?= secret-inventory
CMD_DIR := ./cmd/secret-inventory
OUT_DIR ?= ./bin

help:
	@echo "Targets:"
	@echo "  make tidy        Run go mod tidy"
	@echo "  make build       Build $(BINARY_NAME) into $(OUT_DIR)/"
	@echo "  make clean       Remove $(OUT_DIR)/"

tidy:
	go mod tidy

build:
	@mkdir -p $(OUT_DIR)
	go build -o $(OUT_DIR)/$(BINARY_NAME) $(CMD_DIR)

clean:
	@rm -rf $(OUT_DIR)
