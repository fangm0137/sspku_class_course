package fixtures

import "embed"

// Files contains source samples; A samples are never inserted by the seed command.
//
//go:embed class-a-intro.txt class-a-notes.md class-b-seed.txt
var Files embed.FS
