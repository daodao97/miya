package automation

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"miya/internal/appdir"
)

// A recipe is a ready-made condition: a command with named parameters and a
// sentence that says when it is met. Recipes are files, one each, in
// RecipeDir; adding a file adds a kind of trigger without a new version of
// Miya. The ones Miya comes with are files too (recipes/), read the same
// way, and a file of the same name in RecipeDir replaces one of them.

//go:embed recipes/*.json
var bundledRecipes embed.FS

// Recipe is a condition someone wrote for others to use.
type Recipe struct {
	// Name is how a condition refers to it: the file's name without .json
	// unless the file says otherwise.
	Name string `json:"name"`
	// Description tells a person or an agent when to use it.
	Description string `json:"description,omitempty"`
	// When is success (the default), changed or line.
	When    string        `json:"when,omitempty"`
	Command string        `json:"command"`
	Params  []RecipeParam `json:"params,omitempty"`
	// Text says when it is met, with $NAME for each parameter.
	Text string `json:"text,omitempty"`
	// EveryMinutes is how often a success or changed command runs unless
	// the condition says.
	EveryMinutes int `json:"everyMinutes,omitempty"`
	// Bundled marks a recipe Miya comes with.
	Bundled bool `json:"bundled,omitempty"`
}

// RecipeParam is a value a recipe's command takes, as the environment
// variable Name. One without a Default has to be given.
type RecipeParam struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Default     string `json:"default,omitempty"`
}

// RecipeDir is where people put their recipes.
func RecipeDir() string { return filepath.Join(appdir.Dir(), "triggers") }

var (
	recipeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	paramNamePattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	// reservedParams are variables a shell needs for itself.
	reservedParams = map[string]bool{"PATH": true, "HOME": true, "SHELL": true, "USER": true, "PWD": true, "IFS": true, "ENV": true, "TMPDIR": true}
)

func parseRecipe(file string, data []byte) (Recipe, error) {
	var r Recipe
	if err := json.Unmarshal(data, &r); err != nil {
		return r, err
	}
	r.Name, r.Command, r.When = strings.TrimSpace(r.Name), strings.TrimSpace(r.Command), strings.TrimSpace(r.When)
	r.Text, r.Description = strings.TrimSpace(r.Text), strings.TrimSpace(r.Description)
	if r.Name == "" {
		r.Name = strings.TrimSuffix(file, ".json")
	}
	switch {
	case !recipeNamePattern.MatchString(r.Name):
		return r, fmt.Errorf("the name %q may only have lower-case letters, digits, - and _", r.Name)
	case r.Command == "":
		return r, errors.New("it has no command")
	}
	switch r.When {
	case "":
		r.When = WhenSuccess
	case WhenSuccess, WhenChanged, WhenLine:
	default:
		return r, fmt.Errorf("unknown when %q: use success, changed or line", r.When)
	}
	seen := map[string]bool{}
	for i := range r.Params {
		p := &r.Params[i]
		p.Name, p.Default = strings.TrimSpace(p.Name), strings.TrimSpace(p.Default)
		if !paramNamePattern.MatchString(p.Name) || reservedParams[p.Name] || seen[p.Name] {
			return r, fmt.Errorf("the parameter %q needs a name of its own in upper case, such as BELOW_GB, that the shell does not use itself", p.Name)
		}
		seen[p.Name] = true
	}
	r.EveryMinutes = max(r.EveryMinutes, 0)
	return r, nil
}

// Recipes lists the recipes by name, and says what is wrong with the files
// that could not be used.
func Recipes() ([]Recipe, []string) {
	byName := map[string]Recipe{}
	var problems []string
	add := func(file string, data []byte, bundled bool) {
		r, err := parseRecipe(file, data)
		if err != nil {
			problems = append(problems, file+": "+err.Error())
			return
		}
		r.Bundled = bundled
		byName[r.Name] = r
	}
	if entries, err := bundledRecipes.ReadDir("recipes"); err == nil {
		for _, e := range entries {
			if data, err := bundledRecipes.ReadFile("recipes/" + e.Name()); err == nil {
				add(e.Name(), data, true)
			}
		}
	}
	dir := RecipeDir()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		add(e.Name(), data, false)
	}
	list := make([]Recipe, 0, len(byName))
	for _, r := range byName {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, problems
}

// FindRecipe is the recipe of a name.
func FindRecipe(name string) (Recipe, bool) {
	list, _ := Recipes()
	for _, r := range list {
		if r.Name == name {
			return r, true
		}
	}
	return Recipe{}, false
}

// RecipeHints says what each recipe is for and takes, for the prompts and
// tool descriptions that offer them to an agent.
func RecipeHints() string {
	list, _ := Recipes()
	var parts []string
	for _, r := range list {
		hint := r.Name
		if r.Description != "" {
			hint += ": " + r.Description
		}
		var params []string
		for _, p := range r.Params {
			text := p.Name
			if p.Description != "" {
				text += " = " + p.Description
			}
			if p.Default != "" {
				text += " (default " + p.Default + ")"
			}
			params = append(params, text)
		}
		if len(params) > 0 {
			hint += " [params: " + strings.Join(params, ", ") + "]"
		}
		parts = append(parts, hint)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "; ")
}

// applyRecipe copies a condition's recipe into it and checks the
// parameters. A recipe that is gone leaves a condition that already has its
// command as it was saved.
func (c *Condition) applyRecipe() error {
	r, ok := FindRecipe(c.Recipe)
	if !ok {
		if c.Command != "" {
			return nil
		}
		return fmt.Errorf("Unknown recipe %q. Recipes are the .json files in %s", c.Recipe, RecipeDir())
	}
	params := map[string]string{}
	for _, p := range r.Params {
		value := strings.TrimSpace(c.Params[p.Name])
		if value == "" {
			value = p.Default
		}
		if value == "" {
			return fmt.Errorf("The recipe %s needs %s: %s", r.Name, p.Name, p.Description)
		}
		params[p.Name] = value
	}
	c.Params, c.Command, c.When, c.Text = params, r.Command, r.When, r.Text
	if len(params) == 0 {
		c.Params = nil
	}
	if c.EveryMinutes <= 0 {
		c.EveryMinutes = r.EveryMinutes
	}
	return nil
}

// fromProbe reads a condition written before recipes: its probe is the
// recipe's name, its path the volume and its threshold the amount.
func (c *Condition) fromProbe() {
	probe := strings.TrimSpace(c.Probe)
	if probe != "" && probe != ProbeCommand && strings.TrimSpace(c.Recipe) == "" {
		params := maps.Clone(c.Params)
		if params == nil {
			params = map[string]string{}
		}
		if path := strings.TrimSpace(c.Path); path != "" && params["VOLUME"] == "" {
			params["VOLUME"] = expandHome(path)
		}
		if c.BelowGB > 0 && params["BELOW_GB"] == "" {
			params["BELOW_GB"] = strconv.FormatFloat(c.BelowGB, 'f', -1, 64)
		}
		c.Recipe, c.Params = probe, params
	}
	c.Probe, c.Path, c.BelowGB = "", "", 0
}
