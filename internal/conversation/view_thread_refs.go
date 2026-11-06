package conversation

import (
	appmodel "miya/internal/appmodel"
)

type threadTag struct {
	Ref   appmodel.ThreadRef
	Title string
}
