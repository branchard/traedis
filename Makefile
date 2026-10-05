# @see: https://stackoverflow.com/a/70550568
MAKEFLAGS += --no-print-directory
.PHONY: help start stop clean unwatch unit e2e

##@ Global
help: ## Show this help
	@# @see: https://www.avonture.be/blog/makefile-help/
	@awk 'BEGIN { FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n" } \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

start: unwatch ## Start containers with Docker Compose and Docker Compose Watch
	docker compose up -d --force-recreate
	nohup docker compose watch </dev/null >/dev/null 2>&1 & echo $$! > /tmp/compose-watch.traedis.pid

stop: unwatch ## Stop and delete all containers
	docker compose down

clean: unwatch ## Stop, delete all containers and remove volumes
	docker compose down --remove-orphans --volumes

unwatch: ## Stop Docker Compose Watch
	@if [ -f /tmp/compose-watch.traedis.pid ]; then kill $$(cat /tmp/compose-watch.traedis.pid) 2>/dev/null || true; rm -f /tmp/compose-watch.traedis.pid; fi

unit: export TRAEDIS_REDIS_DSN = redis://localhost:6379/15
unit: stop ## Run unit and Redis integration tests (compiled Go, then Yaegi)
	docker compose up -d --wait --wait-timeout 120 cache
	@# Always stop the containers, even when a test fails
	go vet ./... && go test -race ./... && (cd pkg && yaegi test -v .); status=$$?; docker compose down; exit $$status

e2e: stop ## Run end-to-end tests
	docker compose up -d --wait --wait-timeout 120
	@# Always stop the containers, even when a test fails
	./e2e.sh; status=$$?; docker compose down; exit $$status
