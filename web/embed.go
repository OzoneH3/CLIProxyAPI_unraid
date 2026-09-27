package assets

import (
	"embed"
	"io/fs"
)

//go:embed static/*
var content embed.FS
var Static, _ = fs.Sub(content, "static")
