package main

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

type threadTag struct {
	ID    int
	Title string
}

var threadPacketID = regexp.MustCompile(`\bid="(\d+)"`)

func (app *chatApp) referenceTitle(id int) string {
	if app.referenceThread != nil {
		if t, ok := app.referenceThread(id); ok {
			return threadRefTitle(t)
		}
	}
	if t, _, ok := app.c.findThread(id); ok {
		return threadRefTitle(t)
	}
	return fmt.Sprintf("对话 #%d", id)
}

// History keeps the source title as it was when the reference was sent.
// Leave references at their positions in the sentence and omit context packets.
func (app *chatApp) threadReferenceContent(text string) (string, []threadTag) {
	packets := threadContextPattern.FindAllStringSubmatch(text, -1)
	own := text
	if len(packets) > 0 {
		own, _, _ = strings.Cut(text, threadContextOpen)
	}
	names := map[int]string{}
	var packetIDs []int
	for _, m := range packets {
		header, _, _ := strings.Cut(m[0], ">")
		match := threadPacketID.FindStringSubmatch(header)
		if len(match) != 2 {
			continue
		}
		id, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		title := html.UnescapeString(m[1])
		names[id] = title
		packetIDs = append(packetIDs, id)
		own = strings.ReplaceAll(own, "「对话："+title+"」", "@#"+strconv.Itoa(id))
	}
	ids := threadRefs(own)
	seen := map[int]bool{}
	var tags []threadTag
	for _, id := range append(ids, packetIDs...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		title := names[id]
		if title == "" {
			title = app.referenceTitle(id)
		}
		tags = append(tags, threadTag{ID: id, Title: title})
	}
	return own, tags
}
