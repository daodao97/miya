package integration

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/png"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"miya/internal/agent/cliupdate"
	"miya/internal/chat/model"
	msgfmt "miya/internal/chat/presentation"
	"miya/internal/controller"
	phoneclient "miya/internal/phone/client"
	phoneproto "miya/internal/phone/protocol"
	"miya/internal/uiruntime"
)

var testSecretNotFound = errors.New("test secret not found")

// No view, renderer, native window or installed service participates in domain tests.
func isolatedController(t *testing.T) *controller.Controller {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MIYA_ISOLATED_DIR", t.TempDir())
	uiruntime.InlineDispatch.Store(true)
	t.Cleanup(func() { uiruntime.InlineDispatch.Store(false) })
	c := serverController(t.TempDir(), "")
	c.Ctx, c.Cancel = context.WithCancel(context.Background())
	t.Cleanup(c.Cancel)
	idle := func(ctx context.Context, _, _ string, _ []string) cliupdate.Info {
		<-ctx.Done()
		return cliupdate.Info{}
	}
	c.CheckAgentCLI, c.UpgradeAgentCLI = idle, idle
	c.SetModel("gpt-test")
	c.SetEffort("medium")
	c.SetStatus("Connecting to Codex…")
	return c
}

func testPhoneClient() *phoneclient.Client {
	m := phoneclient.New()
	m.SecretNotFound = testSecretNotFound
	m.Loaded, m.Link, m.Connected, m.Host = true, "miya://connect", true, "mac"
	m.State = phoneproto.State{ReferenceSource: testReferenceSource, Projects: []model.ProjectSnapshot{
		{Name: "proj-a", Path: "/a", Current: true, Threads: []model.Thread{{ID: 1, Title: "thread-one", ServerID: "s1"}}},
		{Name: "proj-b", Path: "/b", Threads: []model.Thread{{ID: 2, Title: "thread-two", ServerID: "s2"}}},
	}}
	return m
}
func activityTitle(m model.Message) string    { return msgfmt.Message(m).Title }
func collaborationJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func init() {
	if os.Getenv("MIYA_WEB_DISCOVERY_HELPER") != "" || os.Getenv("MIYA_COLLABORATION_MCP_HELPER") != "" {
		return
	}
	_, source, _, ok := runtime.Caller(0)
	if ok && filepath.IsAbs(source) {
		if err := os.Chdir(filepath.Join(filepath.Dir(source), "../..")); err != nil {
			panic(err)
		}
	}
}

// Decode the shared image wire format without a renderer or global UI cache.
func decodeTestFileImage(raw string) (image.Config, error) {
	path := raw
	if u, err := url.Parse(raw); err == nil && u.Scheme == "file" {
		path = u.Path
	}
	f, err := os.Open(path)
	if err != nil {
		return image.Config{}, err
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	return config, err
}
