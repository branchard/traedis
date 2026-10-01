# @see: https://stackoverflow.com/a/70550568
MAKEFLAGS += --no-print-directory
.PHONY: help start stop clean

##@ Global
help: ## Show this help
	@# @see: https://www.avonture.be/blog/makefile-help/
	@awk 'BEGIN { FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n" } \
		/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

start: ## Start containers with Docker Compose
	docker compose up -d --build --force-recreate

stop: ## Stop and delete all containers
	docker compose down

clean: ## Stop, delete all containers and remove volumes
	docker compose down --remove-orphans --volumes
