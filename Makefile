.PHONY: test build acceptance

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
