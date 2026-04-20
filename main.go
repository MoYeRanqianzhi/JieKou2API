package main

import (
	"embed"
	"jiekou2api/internal/app"
)

//go:embed static
var assets embed.FS

func main() {
	app.Run(assets)
}
