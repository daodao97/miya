package protocol

import (
	agentusage "miya/internal/agent/quota"
	"miya/internal/chat/model"
	projectgit "miya/internal/project/git"
	"miya/internal/remote"
)

type HostCommands struct {
	Choices []model.ComposerChoice
	Known   bool
}

type HostEntry struct {
	Name, Path string
	Dir        bool
}

type HostRequest struct {
	Action, Project, Path, Text string
	Source                      projectgit.DiffSource
	Snapshot                    projectgit.DiffSnapshot
	File                        projectgit.ChangedFile
	Images                      []remote.Image
	Reset                       *agentusage.ResetAttempt
}

type HostSides struct{ Old, New string }
