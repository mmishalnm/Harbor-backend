package docs

import "embed"

//go:embed openapi.yaml
var OpenAPI []byte

//go:embed index.html
var UI []byte

//go:embed ui/*
var Assets embed.FS
