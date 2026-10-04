# @see: https://stackoverflow.com/a/70550568
MAKEFLAGS += --no-print-directory
.PHONY: help start stop clean

##@ Global
help: ## Show this help
	@# @see: https://www.avonture.be/blog/makefile-help/
	@awk 'BEGIN { FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n" } \
		/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

start: unwatch ## Start containers with Docker Compose
	docker compose up -d --force-recreate
	nohup docker compose watch </dev/null >/dev/null 2>&1 & echo $$! > /tmp/compose-watch.traedis.pid

stop: unwatch ## Stop and delete all containers
	docker compose down

clean: unwatch ## Stop, delete all containers and remove volumes
	docker compose down --remove-orphans --volumes

unwatch:
	@if [ -f /tmp/compose-watch.traedis.pid ]; then kill $$(cat /tmp/compose-watch.traedis.pid) 2>/dev/null || true; rm -f /tmp/compose-watch.traedis.pid; fi
