// Package reelkino — корневой пакет: встроенная статика Mini App.
package reelkino

import "embed"

//go:embed web
var WebFS embed.FS
