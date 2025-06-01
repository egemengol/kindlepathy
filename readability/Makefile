./readability: ./server.ts
	@echo "Building readability executable (make)..."
	@(bun build --compile --minify --sourcemap server.ts --outfile readability)

clean:
	@echo "Removing ./readability/readability (make)..."
	@rm -f ./readability ./.*.bun-build
