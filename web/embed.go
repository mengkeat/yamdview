// Package web embeds the viewer HTML template, CSS, JS, and KaTeX assets.
package web

import (
	"embed"
	"io/fs"

	"github.com/mengkeat/yamdview/internal/server"
)

//go:embed index.html viewer.css viewer.js
var content embed.FS

//go:embed assets/katex/katex.min.css assets/katex/katex.min.js assets/katex/VERSION assets/katex/fonts/*.woff2
var katexFS embed.FS

// LoadAssets loads the embedded web files into a server.Assets struct.
func LoadAssets() (server.Assets, error) {
	indexHTML, err := content.ReadFile("index.html")
	if err != nil {
		return server.Assets{}, err
	}
	viewerCSS, err := content.ReadFile("viewer.css")
	if err != nil {
		return server.Assets{}, err
	}
	viewerJS, err := content.ReadFile("viewer.js")
	if err != nil {
		return server.Assets{}, err
	}

	return server.Assets{
		IndexHTML: string(indexHTML),
		ViewerCSS: string(viewerCSS),
		ViewerJS:  string(viewerJS),
	}, nil
}

// KatexFS returns the embedded filesystem containing KaTeX assets.
// The FS is rooted at assets/katex/ so paths like "katex.min.css",
// "katex.min.js", and "fonts/KaTeX_Main-Regular.woff2" are valid.
func KatexFS() fs.FS {
	sub, err := fs.Sub(katexFS, "assets/katex")
	if err != nil {
		panic("embed: " + err.Error())
	}
	return sub
}
