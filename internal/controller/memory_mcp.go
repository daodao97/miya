package controller

import (
	"encoding/json"
	"errors"
	"strings"

	mcpproto "miya/internal/mcp/protocol"
	"miya/internal/memory"
	"miya/internal/memory/manager"
)

func (c *Controller) McpMemory(r mcpproto.Request) (any, error) {
	if c.Memories().Store == nil {
		return nil, errors.New("Memory requires the Miya background service")
	}
	caller, call, err := c.McpCallerWithArguments(r.Tool, r.Arguments)
	if err != nil {
		return nil, err
	}
	var a manager.Args
	dec := json.NewDecoder(strings.NewReader(string(r.Arguments)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, err
	}
	project, err := memory.ProjectScope(c.ThreadWorkspace(caller.ID))
	if err != nil {
		return nil, err
	}
	scope := project
	switch a.Scope {
	case "", "project":
	case "user":
		scope = memory.UserScope()
	default:
		return nil, errors.New("Invalid memory scope")
	}
	var source memory.Source
	if r.Tool == "remember" {
		source, err = c.Memories().Source(caller, call, a)
		if err != nil {
			return nil, err
		}
	}
	store := c.Memories().Store
	// File IO never waits on the service scheduler.
	return c.Memories().Later(func() (any, error) {
		_, settings, _, err := store.List(project)
		if err != nil {
			return nil, err
		}
		if settings.Disabled {
			return nil, errors.New("Memory tools are disabled for this project. Enable them in memory settings.")
		}
		if scope.Kind == "user" && r.Tool == "forget_memory" && !settings.UseUserPreferences {
			return nil, errors.New("User preferences are not enabled for this project. Enable them in memory settings.")
		}
		switch r.Tool {
		case "search_memory":
			entries, err := store.Search(project, a.Query, a.TaskKey, 5)
			if err != nil {
				return nil, err
			}
			b, _ := json.MarshalIndent(entries, "", "  ")
			return string(b), nil
		case "remember":
			until, err := manager.Expiry(a.ValidUntil)
			if err != nil {
				return nil, err
			}
			if scope.Kind == "user" && a.ID != "" && !settings.UseUserPreferences {
				return nil, errors.New("User preferences are not enabled for this project. Enable them in memory settings.")
			}
			// Existing provenance survives a correction; append its new evidence.
			sources := []memory.Source{source}
			if a.ID != "" {
				entries, _, _, err := store.List(scope)
				if err != nil {
					return nil, err
				}
				for _, e := range entries {
					if e.ID == a.ID {
						sources = append(e.Sources, source)
						if len(sources) > 8 {
							sources = append([]memory.Source{e.Sources[0]}, sources[len(sources)-7:]...)
						}
					}
				}
			}
			e, err := store.Save(scope, memory.Mutation{ID: a.ID, Version: a.Version, Kind: a.Kind, Content: strings.TrimSpace(a.Content), TaskKey: a.TaskKey, Sources: sources, ObservedAt: source.ObservedAt, ValidUntil: until})
			if err != nil {
				return nil, err
			}
			c.Dispatch(func() { c.Memories().Note(caller.ID, e) })
			b, _ := json.Marshal(e)
			return "Miya memory saved. You can undo it in the conversation.\n" + string(b), nil
		case "forget_memory":
			e, err := store.Forget(scope, a.ID, a.Version)
			if err != nil {
				return nil, err
			}
			c.Dispatch(func() { c.Memories().Note(caller.ID, e) })
			return "Miya removed the memory content and stopped recalling it. Existing agent history and backups cannot be deleted.", nil
		}
		return nil, errors.New("Unknown memory tool")
	}), nil
}
