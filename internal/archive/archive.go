// Package archive streams files out of a gzip compressed TAR archive.
package archive

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// WalkFunc is called for every regular file in the archive. The reader is
// only valid until WalkFunc returns.
type WalkFunc func(name string, r io.Reader) error

// Walk reads the archive at path sequentially and calls fn for each regular
// file. The archive is streamed, so files are visited in archive order, which
// keeps each user folder together. Walk stops at the first error returned by
// fn or when ctx is cancelled.
func Walk(ctx context.Context, path string, fn WalkFunc) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		if err := fn(hdr.Name, tr); err != nil {
			return fmt.Errorf("%s: %w", hdr.Name, err)
		}
	}
}
