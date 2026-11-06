package main

import (
	appmodel "miya/internal/appmodel"
	controller "miya/internal/controller"
	uiruntime "miya/internal/uiruntime"
	reflect "reflect"
	testing "testing"
)

func TestProjectOrderRestoresSavedOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	saved := []appmodel.Project{{Name: "b", Path: "/b"}, {Name: "a", Path: "/a"}}
	appmodel.PersistProjectOrder(saved)
	discovered := []appmodel.Project{saved[1], {Name: "new", Path: "/new"}, saved[0]}
	want := append(append([]appmodel.Project(nil), saved...), discovered[1])
	if got := appmodel.RestoreProjectOrder(discovered); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored=%v want=%v", got, want)
	}
	// Removed projects must not reappear from the order file.
	if got := appmodel.RestoreProjectOrder([]appmodel.Project{saved[1]}); !reflect.DeepEqual(got, []appmodel.Project{saved[1]}) {
		t.Fatal(got)
	}
}

func TestProjectOrderTiesKeepSavedOrder(t *testing.T) {
	c := &controller.Controller{WindowState: appmodel.WindowState{Cwd: "/a", ProjectCache: map[string][]appmodel.Thread{}}}
	want := []appmodel.Project{{Path: "/b"}, {Path: "/a"}}
	c.Projects, c.SetProjects = uiruntime.CreateSignal(want)
	c.RefreshProjectOrder()
	if !reflect.DeepEqual(c.Projects(), want) {
		t.Fatal("startup workspace changed saved order")
	}
	c.ProjectCache["/a"] = []appmodel.Thread{{LastActivityAt: 42}}
	c.RefreshProjectOrder()
	if c.Projects()[0].Path != "/a" {
		t.Fatal("new activity did not update order")
	}
}
