package archive

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestZipSingleFile(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.txt")
	os.WriteFile(f, []byte("hello"), 0644)

	zipPath, err := ZipPaths([]string{f})
	if err != nil {
		t.Fatalf("ZipPaths failed: %v", err)
	}
	defer os.Remove(zipPath)

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}
	defer r.Close()

	if len(r.File) != 1 {
		t.Fatalf("expected 1 file in zip, got %d", len(r.File))
	}
	if r.File[0].Name != "test.txt" {
		t.Fatalf("expected test.txt, got %s", r.File[0].Name)
	}
}

func TestZipMultipleFiles(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(tmp, "b.txt"), []byte("bbb"), 0644)

	zipPath, err := ZipPaths([]string{
		filepath.Join(tmp, "a.txt"),
		filepath.Join(tmp, "b.txt"),
	})
	if err != nil {
		t.Fatalf("ZipPaths failed: %v", err)
	}
	defer os.Remove(zipPath)

	r, _ := zip.OpenReader(zipPath)
	defer r.Close()

	if len(r.File) != 2 {
		t.Fatalf("expected 2 files in zip, got %d", len(r.File))
	}
}

func TestZipDirectory(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "mydir")
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested"), 0644)

	zipPath, err := ZipPaths([]string{dir})
	if err != nil {
		t.Fatalf("ZipPaths failed: %v", err)
	}
	defer os.Remove(zipPath)

	r, _ := zip.OpenReader(zipPath)
	defer r.Close()

	if len(r.File) != 2 {
		t.Fatalf("expected 2 files in zip, got %d", len(r.File))
	}

	names := map[string]bool{}
	for _, f := range r.File {
		names[f.Name] = true
	}
	if !names["mydir/root.txt"] {
		t.Fatal("missing mydir/root.txt")
	}
	if !names["mydir/sub/nested.txt"] {
		t.Fatal("missing mydir/sub/nested.txt")
	}
}

func TestZipNonexistent(t *testing.T) {
	_, err := ZipPaths([]string{"/nonexistent/file.txt"})
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}
