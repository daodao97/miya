//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCodexSentImagesStayPreviews(t *testing.T) {
	for _, steer := range []bool{false, true} {
		for _, imageOnly := range []bool{false, true} {
			name := "send"
			if steer {
				name = "steer"
			}
			if imageOnly {
				name += "/image-only"
			} else {
				name += "/mixed-attachments"
			}
			t.Run(name, func(t *testing.T) {
				c, d, _ := codexResumeFixture(t)
				c.resume(c.threadWithID(1), nil)
				waitCodexResume(t, c, d, func() bool { return c.threadWithID(1).Resumed })
				if steer {
					c.submit(SendRequest{ThreadID: 1, Text: "long"})
					waitCodexResume(t, c, d, func() bool { return c.threadWithID(1).TurnID != "" })
				}
				path := filepath.Join(t.TempDir(), "photo with spaces.png")
				if err := os.WriteFile(path, testPNG(t), 0600); err != nil {
					t.Fatal(err)
				}
				req := SendRequest{ThreadID: 1, Steer: steer, Attachments: []string{path}}
				wantText := "Please inspect the attached file(s)."
				if !imageOnly {
					req.Text = "look at this"
					req.Attachments = append(req.Attachments, filepath.Join(t.TempDir(), "notes.txt"))
					wantText = "look at this\n📎 notes.txt"
				}
				if id := c.submit(req); id != 1 {
					t.Fatalf("send returned %d", id)
				}
				messages := c.threadWithID(1).Messages
				sent := messages[len(messages)-1]
				wantImages := []string{localImageDataURI(path)}
				if sent.Content != wantText || !reflect.DeepEqual(sent.Images, wantImages) {
					t.Fatalf("sent message must immediately show previews: %+v", sent)
				}
				if _, err := loadImage(sent.Images[0]); err != nil {
					t.Fatalf("preview unreadable: %v", err)
				}
				waitCodexResume(t, c, d, func() bool {
					for _, m := range c.threadWithID(1).Messages {
						if m.ID == sent.ID {
							return m.ItemID != "pending-user"
						}
					}
					return false
				})
				if !steer {
					waitCodexResume(t, c, d, func() bool { return !c.threadWithID(1).Busy })
				}
				check := func(stage string) {
					t.Helper()
					var users []Message
					for _, m := range c.threadWithID(1).Messages {
						if m.Role == "user" {
							users = append(users, m)
						}
					}
					wantUsers := 2 // Persisted prompt and this send.
					if steer {
						wantUsers++ // Initial prompt in the running turn.
					}
					if len(users) != wantUsers {
						t.Fatalf("%s: unexpected user messages: %+v", stage, users)
					}
					got := users[len(users)-1]
					if !reflect.DeepEqual(got.Images, wantImages) || strings.Contains(got.Content, filepath.Base(path)) {
						t.Fatalf("%s: image preview changed: %+v", stage, got)
					}
				}
				check("after server events")
				c.resume(c.threadWithID(1), nil)
				waitCodexResume(t, c, d, func() bool { return !c.threadWithID(1).Loading })
				check("after history reload")
			})
		}
	}
}
