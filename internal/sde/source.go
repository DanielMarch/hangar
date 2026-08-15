package sde

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// DirSource is a SourceProvider backed by a directory of `<table>.jsonl`
// files — what's left once a downloaded SDE zip has been extracted, and
// exactly what testdata/sde/*.jsonl fixtures are.
type DirSource struct {
	Dir string
}

func (d DirSource) Open(_ context.Context, table string) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Join(d.Dir, table+".jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, NotFound(err)
		}
		return nil, err
	}
	return f, nil
}

// ZipSource is a SourceProvider backed by an already-downloaded SDE zip
// archive. archive/zip needs random access (an io.ReaderAt over the whole
// file), which is why DownloadZip below stages the download to a temp
// file on disk rather than holding it in memory — the "never buffer the
// whole payload in memory" requirement is about the multi-gigabyte
// UNCOMPRESSED JSONL content, not the act of having exactly one compressed
// copy on disk, which is the standard way to read a zip's central
// directory at all.
type ZipSource struct {
	Reader *zip.Reader
}

func (z ZipSource) Open(_ context.Context, table string) (io.ReadCloser, error) {
	name := table + ".jsonl"
	for _, f := range z.Reader.File {
		if filepath.Base(f.Name) == name {
			return f.Open()
		}
	}
	return nil, NotFound(fmt.Errorf("sde: %s not present in zip", name))
}

// OpenZip opens an already-downloaded SDE zip from disk.
//
// PHASE 20.5 (B22): the operator path for an air-gapped or
// bandwidth-constrained installation, where somebody fetches the export once
// and imports it on every host from a share. The zip reader is left open for
// the life of the process, which is the whole of one `hangar admin
// import-sde` run.
func OpenZip(path string) (ZipSource, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return ZipSource{}, fmt.Errorf("sde: opening %s: %w", path, err)
	}
	return ZipSource{Reader: &zr.Reader}, nil
}

// DownloadZip streams an SDE zip from url to a temp file (bounded memory:
// io.Copy moves the body straight to disk in fixed-size chunks, never
// holding the response in a buffer) and opens it as a ZipSource. The
// caller is responsible for removing the returned path once done.
func DownloadZip(ctx context.Context, client *http.Client, url string) (ZipSource, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ZipSource{}, "", fmt.Errorf("sde: building download request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ZipSource{}, "", fmt.Errorf("sde: downloading %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ZipSource{}, "", fmt.Errorf("sde: download of %s returned status %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "hangar-sde-*.zip")
	if err != nil {
		return ZipSource{}, "", fmt.Errorf("sde: creating temp file for download: %w", err)
	}
	path := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(path)
		return ZipSource{}, "", fmt.Errorf("sde: streaming download to %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return ZipSource{}, "", fmt.Errorf("sde: closing downloaded file: %w", err)
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		_ = os.Remove(path)
		return ZipSource{}, "", fmt.Errorf("sde: opening downloaded zip: %w", err)
	}
	return ZipSource{Reader: &zr.Reader}, path, nil
}
