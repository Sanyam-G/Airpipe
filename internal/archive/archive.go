package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ZipPaths bundles the given paths into a temporary zip file and returns its
// path. Symlinks are skipped rather than followed: zipping a symlink to
// /etc/passwd would otherwise include the target file's contents.
func ZipPaths(paths []string) (string, error) {
	tmp, err := os.CreateTemp("", "airpipe-*.zip")
	if err != nil {
		return "", err
	}
	defer func() {
		tmp.Close()
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()

	zw := zip.NewWriter(tmp)

	for _, p := range paths {
		info, lstatErr := os.Lstat(p)
		if lstatErr != nil {
			err = lstatErr
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			err = fmt.Errorf("refusing to archive symlink %q; pass its target explicitly", p)
			return "", err
		}

		if info.IsDir() {
			err = filepath.WalkDir(p, func(fpath string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				fi, infoErr := d.Info()
				if infoErr != nil {
					return infoErr
				}
				if fi.Mode()&os.ModeSymlink != 0 {
					return nil
				}
				if d.IsDir() {
					return nil
				}
				if !fi.Mode().IsRegular() {
					return nil
				}
				rel, relErr := filepath.Rel(filepath.Dir(p), fpath)
				if relErr != nil {
					return relErr
				}
				return addToZip(zw, fpath, rel)
			})
		} else if info.Mode().IsRegular() {
			err = addToZip(zw, p, filepath.Base(p))
		} else {
			err = fmt.Errorf("refusing to archive non-regular file %q", p)
			return "", err
		}

		if err != nil {
			return "", err
		}
	}

	if err = zw.Close(); err != nil {
		return "", err
	}

	return tmp.Name(), nil
}

func addToZip(zw *zip.Writer, srcPath, name string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	w, err := zw.Create(name)
	if err != nil {
		return err
	}

	_, err = io.Copy(w, src)
	return err
}
