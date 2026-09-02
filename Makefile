.PHONY: test build acceptance

test:
	docker build --target test -t photo-browser-test .

build:
	docker build --platform linux/amd64 --target runtime -t photo-browser:local .

acceptance: build
	bash scripts/acceptance.sh
