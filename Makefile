.PHONY: help tidy build clean

BINARY_NAME ?= secret-inventory
CMD_DIR := ./cmd/secret-inventory
OUT_DIR ?= ./bin
REPORT_DIR ?= ./report

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
	@rm -rf $(REPORT_DIR)

run-scan: build
	@mkdir -p $(REPORT_DIR)/run-scan
	$(OUT_DIR)/$(BINARY_NAME) scan --out $(REPORT_DIR)/run-scan --config ~/.config/secret-inventory.yml

run-scan-deep-lacks-perms: build
	@mkdir -p $(REPORT_DIR)/run-scan-deep-lacks-perms
	$(OUT_DIR)/$(BINARY_NAME) scan --out $(REPORT_DIR)/run-scan-deep-lacks-perms --config ~/.config/secret-inventory.yml --deep-inspect

run-scan-deep: build
	@mkdir -p $(REPORT_DIR)/run-scan-deep
	$(OUT_DIR)/$(BINARY_NAME) scan --out $(REPORT_DIR)/run-scan-deep --config ~/.config/secret-inventory-deep-scan.yml --deep-inspect
