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

bundle-guard:
	@echo "checking prod bundle has no testauth code..."
	@if grep -r -l -E "TestAuthProvider|/mint\?sub" web/dist/ >/dev/null 2>&1; then \
		echo "FAIL: testauth code found in prod bundle"; exit 1; \
	fi
	@echo "OK"

e2e-web:
	$(MAKE) dev-web-stack
	cd web && npm ci --prefer-offline --no-audit && npx playwright install --with-deps chromium && npm run e2e
	$(MAKE) dev-web-stack-down
