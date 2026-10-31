package main

import (
	"reflect"
	"testing"
)

func TestProjectOrderRestoresSavedOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	saved := []Project{{Name: "b", Path: "/b"}, {Name: "a", Path: "/a"}}
	persistProjectOrder(saved)
	discovered := []Project{saved[1], {Name: "new", Path: "/new"}, saved[0]}
	want := append(append([]Project(nil), saved...), discovered[1])
	if got := restoreProjectOrder(discovered); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored=%v want=%v", got, want)
	}
	// Removed projects must not reappear from the order file.
	if got := restoreProjectOrder([]Project{saved[1]}); !reflect.DeepEqual(got, []Project{saved[1]}) {
		t.Fatal(got)
	}
}
func TestProjectOrderTiesKeepSavedOrder(t *testing.T) {
	c := &chatController{cwd: "/a", projectCache: map[string][]Thread{}}
	want := []Project{{Path: "/b"}, {Path: "/a"}}
	c.projects, c.setProjects = createSignal(want)
	c.refreshProjectOrder()
	if !reflect.DeepEqual(c.projects(), want) {
		t.Fatal("startup workspace changed saved order")
	}
	c.projectCache["/a"] = []Thread{{LastActivityAt: 42}}
	c.refreshProjectOrder()
	if c.projects()[0].Path != "/a" {
		t.Fatal("new activity did not update order")
	}
}
