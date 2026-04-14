PORT ?= 8079

.PHONY: run kill restart build dev

# Kill any process on the server port
kill:
	@lsof -t -i :$(PORT) | xargs -r kill -9 2>/dev/null || true
	@echo "port $(PORT) cleared"

# Build frontend and run server
run: kill
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
