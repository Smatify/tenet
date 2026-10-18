MODULES := . $(patsubst %/go.mod,%,$(wildcard dialect/*/go.mod))

.PHONY: test test-short lint fmt tidy cover

test:
	@for m in $(MODULES); do (cd $$m && go test -race -count=1 -timeout 10m ./...) || exit 1; done

test-short:
	@for m in $(MODULES); do (cd $$m && go test -short -count=1 -timeout 5m ./...) || exit 1; done

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	@for m in $(MODULES); do (cd $$m && go vet ./...) || exit 1; done
	@./scripts/check-brand.sh

fmt:
	gofmt -w .

tidy:
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

cover:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1
