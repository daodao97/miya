package main

import (
	"miya/internal/account"
	"miya/internal/async"
	"miya/internal/mcpproto"
	"miya/internal/mcpserver"
	"miya/internal/model"
	"miya/internal/phoneapi"
	"miya/internal/svcproto"
)

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

// The phone protocol's data lives in internal/phoneapi.
type (
	phoneState                = phoneapi.State
	phoneProject              = phoneapi.Project
	phoneThreadDelta          = phoneapi.ThreadDelta
	phoneApprovalReply        = phoneapi.ApprovalReply
	phoneFormField            = phoneapi.FormField
	phoneFormSchema           = phoneapi.FormSchema
	phoneModelOption          = phoneapi.ModelOption
	phoneAgentOptions         = phoneapi.AgentOptions
	phoneAgentQuota           = phoneapi.AgentQuota
	phoneQuotaWindow          = phoneapi.QuotaWindow
	phoneMemoryResult         = phoneapi.MemoryResult
	phoneAutomationRunResult  = phoneapi.AutomationRunResult
	phoneAutomationFormResult = phoneapi.AutomationFormResult
	phoneDesktopReply         = phoneapi.DesktopReply
	phoneCollaborationResult  = phoneapi.CollaborationResult
	phoneCommands             = phoneapi.Commands
)

// The account data lives in internal/account.
type (
	accountDisplay      = account.Display
	accountLimits       = account.Limits
	accountIdentity     = account.Identity
	quotaSnapshot       = account.QuotaSnapshot
	quotaWindow         = account.QuotaWindow
	resetCredit         = account.ResetCredit
	resetCreditsSummary = account.ResetCreditsSummary
	accountResetAttempt = account.ResetAttempt
	accountResetAction  = account.ResetAction
)

var (
	sameAccount     = account.SameAccount
	nextResetCredit = account.NextResetCredit
)

// The service protocol lives in internal/svcproto.
type (
	svcRequest       = svcproto.Request
	svcMessages      = svcproto.ThreadMessages
	svcMessage       = svcproto.Envelope
	svcState         = svcproto.State
	svcDraft         = svcproto.Draft
	svcHello         = svcproto.Hello
	svcThreadValue   = svcproto.ThreadValue
	svcTaskAction    = svcproto.TaskAction
	svcRespond       = svcproto.Respond
	svcQueued        = svcproto.Queued
	svcRewind        = svcproto.Rewind
	svcCommandResult = svcproto.CommandResult
	svcCommand       = svcproto.Command
	svcProject       = svcproto.ProjectRequest
	svcWorkspace     = svcproto.WorkspaceResult
	phoneStatus      = svcproto.PhoneStatus
)

const serviceVersion = svcproto.Version

var (
	serviceSocket      = svcproto.Socket
	messageFingerprint = svcproto.MessageFingerprint
)

// A window's connection to the service lives in internal/svcproto too.
type (
	serviceClient = svcproto.Client
	serviceInbox  = svcproto.Inbox
)

const miyaRequestTimeout = svcproto.CardTimeout

var (
	newServiceClient         = svcproto.NewClient
	newWireServiceClient     = svcproto.NewWireClient
	newDetachedServiceClient = svcproto.NewDetachedClient
	newClosedServiceClient   = svcproto.NewClosedClient
	dialService              = svcproto.Dial
	errServiceClosed         = svcproto.ErrClosed
	serviceCallTimeout       = svcproto.CallTimeout
)

// deferred is a reply worked out off the main thread; see async.Reply.
type deferred = async.Reply

// The MCP tool-call data lives in internal/mcpproto.
type (
	mcpTool           = mcpproto.Tool
	mcpRequest        = mcpproto.Request
	mcpThreadInfo     = mcpproto.ThreadInfo
	mcpThreadsResult  = mcpproto.ThreadsResult
	mcpReadResult     = mcpproto.ReadResult
	mcpStatusResult   = mcpproto.StatusResult
	mcpDelegateResult = mcpproto.DelegateResult
	mcpError          = mcpproto.Error
	mcpAutomationArgs = mcpproto.AutomationArgs
)

const mcpCallerUnknown = mcpproto.CallerUnknown

var mcpReadOnly = mcpproto.ReadOnly

// The -mcp process's server lives in internal/mcpserver.
type mcpServer = mcpserver.Server

var (
	newMCPServer     = mcpserver.NewServer
	serveMCP         = mcpserver.Serve
	registerMCPTools = mcpserver.Register
)

// mcpTools are the MCP tools as registered so far; call it after init.
func mcpTools() []mcpTool { return mcpserver.Tools() }
