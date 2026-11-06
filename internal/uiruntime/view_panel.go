package uiruntime

// goAsync runs work that touches the disk or git off the UI thread. Tests
// replace it to run the work in place.
var GoAsync = func(f func()) { go f() }
