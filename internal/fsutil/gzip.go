package fsutil

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
)

func ReadGzipJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer z.Close()
	return json.NewDecoder(z).Decode(v)
}

// writeGzipJSON replaces path atomically: a process ended midway leaves the
// old file, never half of one.
func WriteGzipJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	z, _ := gzip.NewWriterLevel(tmp, gzip.BestSpeed)
	err = json.NewEncoder(z).Encode(v)
	if cerr := z.Close(); err == nil {
		err = cerr
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
