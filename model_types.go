package main

import "miya/internal/model"

// The conversation data lives in internal/model. These aliases keep
// package main's code unchanged while it is split into packages; new
// packages use the model names directly.
type (
	Message             = model.Message
	Thread              = model.Thread
	MessagePart         = model.MessagePart
	ThreadRef           = model.ThreadRef
	SubAgent            = model.SubAgent
	ThreadTask          = model.ThreadTask
	DiffLineStats       = model.DiffLineStats
	MemoryNotice        = model.MemoryNotice
	QueuedMessage       = model.QueuedMessage
	SendRequest         = model.SendRequest
	Project             = model.Project
	collaborationStatus = model.CollaborationStatus
	composerChoice      = model.ComposerChoice
)

var SourceIDPattern = model.SourceIDPattern
