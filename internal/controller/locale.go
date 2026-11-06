package controller

import "miya/locales"

func tr(english string) string { return locales.T(english) }
