package main

import "lan-copy/internal/app"

// Release builds set these with -ldflags. Updates require a pinned key.
var version = "dev"
var updatePublicKey string

func run() { app.Run(webFiles, version, updatePublicKey) }
