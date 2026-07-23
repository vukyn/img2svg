// Package web embeds the static UI served by the server.
package web

import "embed"

//go:embed static/*
var Static embed.FS
