.PHONY: check

check:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "gofmt would change these files:"; \
		echo "$$files"; \
		exit 1; \
	fi
	go vet ./...
	go test -race ./...
