package archive

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
)

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
		info, statErr := os.Stat(p)
		if statErr != nil {
			err = statErr
			return "", err
		}

		if info.IsDir() {
			err = filepath.Walk(p, func(fpath string, fi os.FileInfo, walkErr error) error {
				if walkErr != nil || fi.IsDir() {
					return walkErr
				}
				rel, _ := filepath.Rel(filepath.Dir(p), fpath)
				return addToZip(zw, fpath, rel)
			})
		} else {
			err = addToZip(zw, p, filepath.Base(p))
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
