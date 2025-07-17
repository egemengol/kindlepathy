package core

import (
	"bytes"
	"fmt"
	"io"
	"net/url"

	"github.com/andybalholm/brotli"
)

// ResolveURL takes a base absolute URL (e.g. "https://example.com/foo/bar")
// and an actual target URL (which can be absolute or relative),
// and returns the absolute resolved form.
func ResolveURL(baseAbsURL string, actualURL string) (string, error) {
	base, err := url.Parse(baseAbsURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(actualURL)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

// RelativizeURL takes an absolute URL (e.g. "https://example.com/foo/bar?x=1#sec")
// and returns the relative path with query and fragment (e.g. "/foo/bar?x=1#sec").
func RelativizeURL(absURL string) string {
	u, _ := url.Parse(absURL)
	rel := u.Path
	if u.RawQuery != "" {
		rel += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		rel += "#" + u.Fragment
	}
	return rel
}

// CompressHTML compresses HTML content using Brotli compression
func CompressHTML(html string) ([]byte, error) {
	if html == "" {
		return nil, nil
	}

	var buf bytes.Buffer
	writer := brotli.NewWriter(&buf)

	_, err := writer.Write([]byte(html))
	if err != nil {
		return nil, fmt.Errorf("failed to write to brotli compressor: %w", err)
	}

	err = writer.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close brotli compressor: %w", err)
	}

	return buf.Bytes(), nil
}

// DecompressHTML decompresses Brotli-compressed HTML content
func DecompressHTML(compressed []byte) (string, error) {
	if compressed == nil || len(compressed) == 0 {
		return "", nil
	}

	reader := brotli.NewReader(bytes.NewReader(compressed))

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to decompress brotli content: %w", err)
	}

	return string(decompressed), nil
}
