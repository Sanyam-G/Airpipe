package web

import (
	"embed"
	"io/fs"
)

//go:embed assets
var assetsFS embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}
	return sub
}
