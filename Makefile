# The Go server loads .env via godotenv. Do not include/export the full file
# here: make treats quoted dotenv values as literal bytes.
ENV_PORT_RAW := $(shell sed -n 's/^[[:space:]]*PORT[[:space:]]*=[[:space:]]*//p' .env 2>/dev/null | tail -n 1 | sed 's/[[:space:]]*\#.*$$//; s/^[[:space:]]*//; s/[[:space:]]*$$//')
ENV_PORT := $(subst ',,$(subst ",,$(ENV_PORT_RAW)))
PORT ?= $(if $(ENV_PORT),$(ENV_PORT),8079)

.PHONY: run kill restart build dev smoke

# Kill server and any orphaned claude CLI subprocesses
kill:
	@lsof -t -i :$(PORT) | xargs -r kill -9 2>/dev/null || true
	@pkill -9 -f "claude.*--print.*cartledger" 2>/dev/null || true
	@echo "port $(PORT) cleared"

# Build frontend and run server
run: kill build
	go run ./cmd/server

# Alias for run
restart: run

# Build frontend for production
build:
	cd web && npm run build

# Run frontend dev server + backend concurrently
dev: kill
	@echo "starting backend on :$(PORT) and frontend on :5173"
	@cd web && npm run dev &
	@go run ./cmd/server

# End-to-end self-hosting smoke test. Builds the binary, walks the bootstrap →
# setup → login → profile → backup → restore → re-login flow against a fresh
# DATA_DIR. Uses the mock LLM client (no API key required). VERBOSE=1 for
# detailed output. See scripts/smoke.sh.
smoke:
	@./scripts/smoke.sh
