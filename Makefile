
tests: test-go test-ts

test-go:
	go test ./...

test-ts:
	cd ts && npm test
	npx vitest run
