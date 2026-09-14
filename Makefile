APP_VERSION := $(shell cat deploy/VERSION)
export APP_VERSION
PRODCHECK := deploy/compose/docker-compose.prodcheck.yml

.PHONY: test build acceptance api-acceptance

test:
	docker build --target test -t photo-browser-test .

build:
	docker build --platform linux/amd64 --target runtime -t photo-browser:local .

acceptance:
	docker build --target acceptance -t photo-browser-acceptance .
	docker run --rm \
	  --name photo-browser-acceptance-$$$$ \
	  -v "$(CURDIR)/test-photos:/fixtures:ro" \
	  -v "$(CURDIR)/scripts:/scripts:ro" \
	  photo-browser-acceptance \
	  bash /scripts/acceptance.sh

api-acceptance:
	docker build --target api-acceptance -t photo-browser-api-acceptance .
	bash scripts/acceptance_api.sh

.PHONY: dev-web-stack dev-web-stack-down test-web e2e-web build-web bundle-guard

dev-web-stack:
	docker build --target api-acceptance -t photo-browser-api-acceptance .
	bash deploy/compose/dev-fixtures.sh
	docker compose -f deploy/compose/docker-compose.dev.yml up -d
	@echo "backend/nginx on :8081, testauth on :8090"
	@echo "next: cd web && npm run dev"

dev-web-stack-down:
	docker compose -f deploy/compose/docker-compose.dev.yml down

test-web:
	cd web && npm ci --prefer-offline --no-audit && npm run test

build-web:
	cd web && npm ci --prefer-offline --no-audit && npm run build

# Builds in prod (firebase) mode with stub config, then asserts the emitted
# bundle carries no testauth code. Leaves web/dist holding the prod build.
bundle-guard:
	cd web && VITE_AUTH_MODE=firebase \
	  VITE_FIREBASE_API_KEY=stub VITE_FIREBASE_AUTH_DOMAIN=stub \
	  VITE_FIREBASE_PROJECT_ID=stub VITE_FIREBASE_APP_ID=stub \
	  npm run build && npm run guard

e2e-web:
	$(MAKE) dev-web-stack
	cd web && npm ci --prefer-offline --no-audit && npx playwright install --with-deps chromium && npm run e2e
	$(MAKE) dev-web-stack-down

.PHONY: preflight

preflight:
	bash scripts/nas-preflight.sh --out docs/deploy/preflight-report.md

.PHONY: build-prod verify-image

# Reproducible, conservatively targeted image for the Braswell NAS.
# GOAMD64=v1 is set in the Dockerfile and must not be relaxed.
build-prod:
	docker build --platform linux/amd64 --target runtime \
	  -t photo-browser:$(APP_VERSION) -t photo-browser:latest-prod .
	@echo "built photo-browser:$(APP_VERSION)"

verify-image:
	bash scripts/verify-image.sh photo-browser:$(APP_VERSION)

.PHONY: prodcheck-up prodcheck-down

# Production image + production nginx.conf, runnable on a laptop.
prodcheck-up: build-prod
	bash deploy/compose/dev-fixtures.sh
	docker compose -f $(PRODCHECK) up -d
	docker compose -f $(PRODCHECK) run --rm --no-deps photo-app index
	docker compose -f $(PRODCHECK) run --rm --no-deps photo-app admin add-user --uid=admin-1 --email=admin@example.com --role=admin
	docker compose -f $(PRODCHECK) run --rm --no-deps photo-app admin add-user --uid=member-1 --email=member@example.com --role=member
	@echo "prodcheck on http://localhost:8088 (Host: photos-api.localhost), testauth on :8090"

prodcheck-down:
	docker compose -f $(PRODCHECK) down -v

.PHONY: verify-deployment

verify-deployment:
	@MEMBER=$$(curl -s "http://localhost:8090/mint?sub=member-1&email=member@example.com&verified=1"); \
	 ADMIN=$$(curl -s "http://localhost:8090/mint?sub=admin-1&email=admin@example.com&verified=1"); \
	 DENIED=$$(curl -s "http://localhost:8090/mint?sub=denied-1&email=denied@example.com&verified=1"); \
	 bash scripts/verify-deployment.sh --base-url http://localhost:8088 --host photos-api.localhost \
	   --member-token "$$MEMBER" --admin-token "$$ADMIN" --denied-token "$$DENIED" \
	   --compose $(PRODCHECK)
