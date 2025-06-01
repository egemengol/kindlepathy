build-readability:
    (cd readability && make ./readability)

test: build-readability
    @echo "Running Go tests (just)..."
    @go test -v ./...

clean:
    @(cd readability && make clean)
