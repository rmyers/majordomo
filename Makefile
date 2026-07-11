default: help

dev:  ## Run the web server locally
	go run ./cmd/majordomo $(args)

build:  ## Build the binary
	go build -o bin/majordomo ./cmd/majordomo

# ============================================
# Help
# ============================================

help:  ## Show available commands
	@grep -h '^[a-zA-Z]' $(MAKEFILE_LIST) | awk -F ':.*?## ' 'NF==2 {printf "   %-15s%s\n", $$1, $$2}' | sort

.PHONY: help dev build
