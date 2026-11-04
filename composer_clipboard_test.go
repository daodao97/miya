package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func TestComposerPastesAttachmentsBeforeClipboardText(t *testing.T) {
	var encoded bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 180, A: 255})
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	paths := []string{filepath.Join(root, "notes with spaces.md"), filepath.Join(root, "测试.txt")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := transfer.FileData(paths...)
	if err != nil {
		t.Fatal(err)
	}
	urls, err := files.Read(transfer.FileList)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"keyboard", "native-menu", "match-style", "context-menu"} {
		for _, kind := range []string{"image", "image-with-text", "files-with-text-and-thumbnail"} {
			t.Run(method+"/"+kind, func(t *testing.T) {
				app, tt := testApp(t)
				app.c.setThreads([]Thread{{ID: 1, Title: "Current: ", HistoryLoaded: true}, handoffSource()})
				app.c.setActive(1)
				app.c.rememberDraft(referenceText(testReference(handoffSource()), "登录修复") + " 保留这段正文")
				tt.Frame()
				doc := app.complete.input.doc
				doc.SetSelection(2, 4)
				tt.Frame()
				version := doc.Version()
				var reps []transfer.Representation
				reps = append(reps, transfer.Bytes(transfer.PNG, encoded.Bytes()))
				if kind != "image" {
					reps = append(reps, transfer.Bytes(transfer.Text, []byte("clipboard fallback text")))
				}
				if kind == "files-with-text-and-thumbnail" {
					reps = append(reps, transfer.Bytes(transfer.FileList, urls))
				}
				tt.SetClipboardData(transfer.New(transfer.NewItem(reps...)))
				if method == "keyboard" {
					tt.Key(ui.Cmd, ui.KeyV)
				} else if method == "native-menu" {
					tt.Command("paste")
				} else if method == "match-style" {
					tt.Command("pasteAndMatchStyle")
				} else {
					if err := tt.RightClick("Message Codex"); err != nil {
						t.Fatal(err)
					}
					if err := tt.ChooseMenuItem("Paste"); err != nil {
						t.Fatal(err)
					}
				}
				tt.Frame()
				attached := app.c.attachments()
				if kind == "files-with-text-and-thumbnail" {
					if !slices.Equal(attached, paths) {
						t.Fatalf("file representations were not preferred: %v", attached)
					}
				} else {
					if len(attached) != 1 {
						t.Fatalf("image did not become an attachment: %v", attached)
					}
					t.Cleanup(func() { os.Remove(attached[0]) })
					data, err := os.ReadFile(attached[0])
					if err != nil || !bytes.Equal(data, encoded.Bytes()) {
						t.Fatalf("pasted image data changed: %v", err)
					}
				}
				if app.c.draft() != referenceText(testReference(handoffSource()), "登录修复")+" 保留这段正文" || doc.Version() != version {
					t.Fatalf("attachment paste changed the prompt or its undo history: %q", app.c.draft())
				}
			})
		}
	}
}

func TestComposerDoesNotDrawKeyboardFocusBorder(t *testing.T) {
	app, tt := testApp(t)
	app.appearance = "light"
	app.applyAppearance()
	// A distinct color makes the default focus ring visible to this pixel check.
	app.theme.Focus = ui.Hex("#ff00ff")
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Type("检查输入框的边框")
	tt.Frame()
	if !tt.Focused("Message Codex") {
		t.Fatal("test must leave keyboard focus in the composer")
	}
	r, ok := tt.Find("Message Codex")
	if !ok {
		t.Fatal("composer missing")
	}
	img := tt.Image()
	for y := max(0, int(r.Y)-5); y < min(img.Bounds().Dy(), int(r.Y+r.H)+5); y++ {
		for x := max(0, int(r.X)-5); x < min(img.Bounds().Dx(), int(r.X+r.W)+5); x++ {
			c := img.RGBAAt(x, y)
			if c.R > 220 && c.B > 220 && c.G < 80 {
				t.Fatal("composer drew the default keyboard focus border")
			}
		}
	}
	if dir := os.Getenv("MIYA_THREAD_TAG_SNAPSHOT"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "composer-keyboard-focus.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, img)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
