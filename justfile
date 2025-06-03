build-readability:
    (cd readability && make ./readability)

test: build-readability
    @echo "Running Go tests (just)..."
    @go test -v ./...

clean:
    @(cd readability && make clean)

img:
    #!/bin/bash
    for size in 16 32 128 256 512
    do
      inkscape --export-type=png --export-filename="web/static/icon-${size}.png" --export-width=$size --export-height=$size web/static/book-open-backg.svg
    done
