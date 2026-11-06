package integration

import (
	"reflect"
	"testing"

	"miya/internal/appmodel"
	"miya/internal/chat/model"
	"miya/internal/controller"
	projectmodel "miya/internal/project/model"
	projectregistry "miya/internal/project/registry"
	"miya/internal/uiruntime"
)

func TestProjectOrderRestoresSavedOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	saved := []projectmodel.Project{{Name: "b", Path: "/b"}, {Name: "a", Path: "/a"}}
	projectregistry.SaveProjectOrder(saved)
	discovered := []projectmodel.Project{saved[1], {Name: "new", Path: "/new"}, saved[0]}
	want := append(append([]projectmodel.Project(nil), saved...), discovered[1])
	if got := projectregistry.RestoreProjectOrder(discovered); !reflect.DeepEqual(got, want) {
		t.Fatalf("restored=%v want=%v", got, want)
	}
	// Removed projects must not reappear from the order file.
	if got := projectregistry.RestoreProjectOrder([]projectmodel.Project{saved[1]}); !reflect.DeepEqual(got, []projectmodel.Project{saved[1]}) {
		t.Fatal(got)
	}
}

func TestProjectOrderTiesKeepSavedOrder(t *testing.T) {
	c := &controller.Controller{WindowState: appmodel.WindowState{Cwd: "/a", ProjectCache: map[string][]model.Thread{}}}
	want := []projectmodel.Project{{Path: "/b"}, {Path: "/a"}}
	c.Projects, c.SetProjects = uiruntime.CreateSignal(want)
	c.RefreshProjectOrder()
	if !reflect.DeepEqual(c.Projects(), want) {
		t.Fatal("startup workspace changed saved order")
	}
	c.ProjectCache["/a"] = []model.Thread{{LastActivityAt: 42}}
	c.RefreshProjectOrder()
	if c.Projects()[0].Path != "/a" {
		t.Fatal("new activity did not update order")
	}
}
