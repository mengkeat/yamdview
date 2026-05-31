// Package web embeds the viewer HTML template, CSS, and JS assets.
package web

import (
	"embed"

	"github.com/mengkeat/yamdview/internal/server"
)

//go:embed index.html viewer.css viewer.js
var content embed.FS

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
