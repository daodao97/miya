package locales

import (
	"embed"

	"miya/internal/i18ncatalog"
)

//go:generate go run ./gen -dir .

//go:embed *.json
var files embed.FS

// Catalog holds Miya's messages independently of either UI framework.
var Catalog = func() *i18ncatalog.Catalog {
	catalog, err := i18ncatalog.New(files, "en")
	if err != nil {
		panic(err)
	}
	return catalog
}()
