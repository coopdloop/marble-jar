COMPOSE := docker compose

.PHONY: dev dev-build down restart logs ps clean \
	test-core typecheck-web typecheck-sdk check

# --- Dev stack ---------------------------------------------------------------

# Bring up every dev resource (postgres, redis, redpanda, hydra, phoenix,
# core, dispatch-worker, mcp-bridge, web) in the background.
dev:
	$(COMPOSE) up -d
	$(COMPOSE) ps

# Same as dev, but rebuild images first (after changing Go/Python code
# or Dockerfiles).
dev-build:
	$(COMPOSE) up -d --build
	$(COMPOSE) ps

# Tear the stack down (keep volumes; use `make clean` to wipe them).
down:
	$(COMPOSE) down

restart:
	$(COMPOSE) restart

# Tail all logs: `make logs SVC=core` for a single service.
logs:
	$(COMPOSE) logs -f $(SVC)

ps:
	$(COMPOSE) ps

# Stop everything and remove volumes (drops the DB).
clean:
	$(COMPOSE) down -v

# --- Repo checks -------------------------------------------------------------

# Skips automatically when no MARBLEJAR_TEST_DATABASE_URL is set.
test-core:
	cd services/core && go test ./...

typecheck-web:
	cd apps/web && npm run typecheck

typecheck-sdk:
	cd packages/sdk-ts && npm run typecheck

check: test-core typecheck-web typecheck-sdk
