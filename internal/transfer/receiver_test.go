package transfer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUniquePathNoConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.pdf")
	result := uniquePath(path)
	if result != path {
		t.Fatalf("expected %s, got %s", path, result)
	}
}

func TestUniquePathSingleConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.pdf")
	os.WriteFile(path, []byte("existing"), 0644)

	result := uniquePath(path)
	expected := filepath.Join(dir, "report(1).pdf")
	if result != expected {
		t.Fatalf("expected %s, got %s", expected, result)
	}
}

func TestUniquePathMultipleConflicts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	os.WriteFile(path, []byte("v1"), 0644)
	os.WriteFile(filepath.Join(dir, "data(1).csv"), []byte("v2"), 0644)
	os.WriteFile(filepath.Join(dir, "data(2).csv"), []byte("v3"), 0644)

	result := uniquePath(path)
	expected := filepath.Join(dir, "data(3).csv")
	if result != expected {
		t.Fatalf("expected %s, got %s", expected, result)
	}
}

func TestUniquePathNoExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	os.WriteFile(path, []byte("existing"), 0644)

	result := uniquePath(path)
	expected := filepath.Join(dir, "Makefile(1)")
	if result != expected {
		t.Fatalf("expected %s, got %s", expected, result)
	}
}

func TestReceiveFileBadDir(t *testing.T) {
	r := &Receiver{}
	_, err := r.ReceiveFile("/nonexistent/path/that/doesnt/exist", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestReceiveFileNotADir(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "afile.txt")
	os.WriteFile(filePath, []byte("hi"), 0644)

	r := &Receiver{}
	_, err := r.ReceiveFile(filePath, nil)
	if err == nil {
		t.Fatal("expected error when dest is a file, not a directory")
	}
}
