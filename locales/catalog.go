package locales

import (
	"embed"
	"io/fs"

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

// CatalogFiles exposes the embedded, read-only catalogs to the legacy MyGo UI
// adapter, which registers its own catalog and widget translations.
func CatalogFiles() fs.FS { return files }
