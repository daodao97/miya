package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"miya/internal/claude"
	"miya/internal/codex"
)

// An automation is written as one sentence; the person's agent turns it into
// settings in one call with no tools and a JSON schema for its answer, then
// Miya checks what it made.

type automationDraft struct {
	Name         string `json:"name"`
	Schedule     string `json:"schedule"`
	Check        string `json:"check"`
	Rounds       int    `json:"rounds"`
	Instructions string `json:"instructions"`
	TimeMissing  bool   `json:"time_missing"`
}

const automationDraftSchema = `{"type":"object","additionalProperties":false,` +
	`"required":["name","schedule","check","rounds","instructions","time_missing"],` +
	`"properties":{"name":{"type":"string"},"schedule":{"type":"string"},"check":{"type":"string"},` +
	`"rounds":{"type":"integer"},"instructions":{"type":"string"},"time_missing":{"type":"boolean"}}}`

// defaultAutomationSchedule is the time of a sentence that names none.
const defaultAutomationSchedule = "0 9 * * *"

func automationDraftPrompt(request, project string, now time.Time) string {
	zone, _ := now.Zone()
	weekdays := []string{"日", "一", "二", "三", "四", "五", "六"}
	return fmt.Sprintf(`你在帮 Miya 把一句话整理成定时自动化的设置。只回答符合 schema 的 JSON，不要执行任何操作。

当前时间：%s 周%s（时区 %s）
项目目录：%s
项目顶层的文件：%s

规则：
- name：6 到 14 个字的中文名称，说明做什么。
- schedule：5 段 cron（分 时 日 月 周），按本地时间。句子没说时间时填 "%s"，并把 time_missing 设为 true。
- check：句子要求测试通过、构建成功等可以用命令验证的结果时，给出在项目目录执行的一条完整 shell 命令，按项目文件推断，例如有 go.mod 用 go test ./...，有 package.json 用 npm test。命令会在项目目录里执行，不要写 cd。句子没要求验证时留空。
- rounds：有 check 时最多跑几轮，1 到 5。「多试两次」是 3 轮，没说时是 3。没有 check 时是 1。
- instructions：每次运行时发给 coding agent 的第一条消息。用中文写清要做什么、做到什么程度算完成、没有事情可做时回复「无事可报」。不要写运行时间，也不要写检查命令和重试规则，这些由 Miya 负责。

句子：%s`,
		now.Format("2006-01-02 15:04"), weekdays[now.Weekday()], zone, project, projectTopFiles(project), defaultAutomationSchedule, request)
}

// projectTopFiles lists a project's top-level entries, which tell its
// language and build tools.
func projectTopFiles(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "（无法读取）"
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && name != ".github" {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 40 {
		names = append(names[:40], "…")
	}
	if len(names) == 0 {
		return "（空）"
	}
	return strings.Join(names, ", ")
}

// draftConfig is how this Miya runs Claude Code, which the call reuses.
type draftConfig struct {
	claudeBinary          string
	claudeArgs, claudeEnv []string
}

// draftAutomation asks agent to turn request into settings. It runs
// programs and waits for them: call it off the main thread.
func draftAutomation(ctx context.Context, cfg draftConfig, agent, project, request string, now time.Time) (automationDraft, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	prompt := automationDraftPrompt(request, project, now)
	var raw []byte
	var err error
	if agent == agentClaude {
		raw, err = draftWithClaude(ctx, cfg, project, prompt)
	} else {
		raw, err = draftWithCodex(ctx, project, prompt)
	}
	if err != nil {
		return automationDraft{}, err
	}
	var d automationDraft
	if err := json.Unmarshal(raw, &d); err != nil {
		return automationDraft{}, fmt.Errorf("agent 的回答不是预期的格式：%v", err)
	}
	return d, nil
}

func draftWithClaude(ctx context.Context, cfg draftConfig, project, prompt string) ([]byte, error) {
	bin := cfg.claudeBinary
	if bin == "" {
		var err error
		if bin, err = claude.Binary(); err != nil {
			return nil, err
		}
	}
	args := append(append([]string(nil), cfg.claudeArgs...), "-p", prompt, "--json-schema", automationDraftSchema, "--output-format", "json", "--tools", "")
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = project
	cmd.Env = append(os.Environ(), cfg.claudeEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Claude Code 整理失败：%v %s", err, clipRunes(strings.TrimSpace(stderr.String()), 300))
	}
	return claudeStructuredOutput(out)
}

// claudeStructuredOutput is the answer of a Claude Code --json-schema run.
func claudeStructuredOutput(out []byte) ([]byte, error) {
	var r struct {
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("Claude Code 的输出无法解析：%v", err)
	}
	if r.IsError {
		return nil, errors.New("Claude Code 整理失败：" + clipRunes(r.Result, 300))
	}
	if len(r.StructuredOutput) > 0 && string(r.StructuredOutput) != "null" {
		return r.StructuredOutput, nil
	}
	return []byte(r.Result), nil
}

func draftWithCodex(ctx context.Context, project, prompt string) ([]byte, error) {
	bin, err := codex.Binary()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "miya-draft-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	schema, last := filepath.Join(dir, "schema.json"), filepath.Join(dir, "last.json")
	if err := os.WriteFile(schema, []byte(automationDraftSchema), 0o600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--output-schema", schema, "-o", last, prompt)
	cmd.Dir = project
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Codex 整理失败：%v %s", err, clipRunes(lastLine(stderr.String()), 300))
	}
	return os.ReadFile(last)
}

// automationFromDraft checks what the agent made and fills the rest.
func automationFromDraft(d automationDraft, agent, project, request string) (Automation, bool, error) {
	a := Automation{Name: d.Name, Project: project, Agent: agent, Request: strings.TrimSpace(request),
		Instructions: d.Instructions, Schedule: d.Schedule, Check: d.Check, Rounds: d.Rounds}
	a.Check = stripProjectCd(a.Check, project)
	timeMissing := d.TimeMissing || strings.TrimSpace(d.Schedule) == ""
	if timeMissing {
		a.Schedule = defaultAutomationSchedule
	}
	if _, err := parseCron(a.Schedule); err != nil {
		return Automation{}, false, fmt.Errorf("整理出的时间 %q 无效，请写得更具体", a.Schedule)
	}
	if strings.TrimSpace(a.Instructions) == "" {
		a.Instructions = a.Request
	}
	if strings.TrimSpace(a.Name) == "" {
		a.Name = clipRunes(firstLine(a.Request), 14)
	}
	if err := a.normalize(); err != nil {
		return Automation{}, false, err
	}
	return a, timeMissing, nil
}

// stripProjectCd drops a leading cd into the project, where checks run
// anyway.
func stripProjectCd(check, project string) string {
	check = strings.TrimSpace(check)
	for _, dir := range []string{project, "'" + project + "'", `"` + project + `"`, "."} {
		if rest, ok := strings.CutPrefix(check, "cd "+dir+" && "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return check
}
