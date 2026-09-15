package web

import "embed"

// Assets contains the pinned browser runtime shipped inside the Jikko binary.
//
//go:embed assets/*
var Assets embed.FS
