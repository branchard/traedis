# @see: https://stackoverflow.com/a/70550568
MAKEFLAGS += --no-print-directory
.PHONY: help start stop reset unwatch unit e2e bench bench-view assets

##@ Global
help: ## Show this help
	@# @see: https://www.avonture.be/blog/makefile-help/
	@awk 'BEGIN { FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n\nTargets:\n" } \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

start: unwatch ## Start containers with Docker Compose and Docker Compose Watch
	docker compose up -d --force-recreate
	nohup docker compose watch </dev/null >/dev/null 2>&1 & echo $$! > /tmp/compose-watch.traedis.pid
	@echo "Open http://localhost:8080/whoami to see whoami page without caching"
	@echo "Open http://localhost:8080/whoami-cache to see whoami page with caching"
	@echo "Open http://localhost:8080/placeholder/800x600 to see sample image without caching"
	@echo "Open http://localhost:8080/placeholder-cache/800x600 to see sample image with caching"
	@echo "Open http://localhost:8080/imaging/unsafe/rs:fill:300:200/plain/http://placeholder:3000/800x600@avif to see sample image through imgproxy without caching"
	@echo "Open http://localhost:8080/imaging-cache/unsafe/rs:fill:300:200/plain/http://placeholder:3000/800x600@avif to see sample image through imgproxy with caching"
	@echo "Open http://localhost:5540/ to see what is stored on Redis"

stop: unwatch ## Stop and delete all containers
	docker compose down

reset: unwatch ## Stop, delete all containers and remove volumes
	docker compose down --remove-orphans --volumes

unwatch: ## Stop Docker Compose Watch
	@if [ -f /tmp/compose-watch.traedis.pid ]; then kill $$(cat /tmp/compose-watch.traedis.pid) 2>/dev/null || true; rm -f /tmp/compose-watch.traedis.pid; fi

unit: export TRAEDIS_REDIS_DSN = redis://localhost:6379/15
unit: stop ## Run unit and Redis integration tests (compiled Go, then Yaegi)
	docker compose up -d --wait --wait-timeout 120 cache
	@# Always stop the containers, even when a test fails
	go vet ./... && go test -race ./... && (cd pkg && yaegi test -v .); status=$$?; docker compose down; exit $$status

e2e: stop ## Run end-to-end tests (Hurl through Traefik)
	docker compose up -d --wait --wait-timeout 120
	@# Always stop the containers, even when a test fails
	./e2e/run.sh; status=$$?; docker compose down; exit $$status

bench: stop ## Run benchmarks (under Yaegi, then k6 through Traefik) into benchmark/results.json
	docker compose up -d --wait --wait-timeout 120 ingress cache whoami placeholder imaging
	@# Always stop the containers, even when a benchmark fails
	./benchmark/run.sh; status=$$?; docker compose down; exit $$status

bench-view: ## Show benchmark/results.json as tables → http://localhost:8082/visualizer.html (Ctrl-C to stop)
	@echo "http://localhost:8082/visualizer.html"
	-docker compose run --rm --service-ports visualizer

assets: ## Optimize icon.svg with SVGO and generate icon.png
	@echo "Optimize SVG…"
	docker run -u "$$(id -u):$$(id -g)" --rm -v "./assets/icon.svg:/assets/icon.svg" oven/bun:alpine bunx --bun svgo \
	--pretty --multipass "/assets/icon.svg" --output "/assets/icon.svg"
	@echo "Generate PNG…"
	docker run -u "$$(id -u):$$(id -g)" --rm -v "./assets:/assets" linuxserver/inkscape inkscape "/assets/icon.svg" \
	--export-type=png --export-png-antialias=3 --export-filename="/assets/icon.png" --export-area-drawing --export-height=256
	@echo "Center PNG…"
	docker run -u "$$(id -u):$$(id -g)" --rm -v "./assets:/assets" dpokidov/imagemagick /assets/icon.png \
    -gravity center -background none -extent 256x256 /assets/icon.png
