package main

import (
	"miya/internal/account"
	"miya/internal/async"
	"miya/internal/automation"
	"miya/internal/claude"
	"miya/internal/claude/transcript"
	"miya/internal/codex"
	"miya/internal/composer"
	"miya/internal/delegation/coordinator"
	"miya/internal/mcpconfig"
	"miya/internal/mcpproto"
	"miya/internal/mcpserver"
	"miya/internal/model"
	"miya/internal/msgfmt"
	"miya/internal/phone"
	"miya/internal/phoneapi"
	"miya/internal/prefs"
	"miya/internal/runtime/tracker"
	"miya/internal/svcproto"
	"miya/internal/threadlist"
	"miya/internal/workspaces"
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
	composerMention     = model.Mention
)

var SourceIDPattern = model.SourceIDPattern

var (
	composerMentions = model.Mentions
	MessageParts     = model.MessageParts
)

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
	sameAccount         = account.SameAccount
	nextResetCredit     = account.NextResetCredit
	formatCreditBalance = account.FormatBalance
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

// Claude Code transcripts are read by internal/claude/transcript.
type (
	claudeBlock = transcript.Block
)

var (
	applyClaudeAgentTasks   = transcript.ApplyAgentTasks
	claudeBlocks            = transcript.Blocks
	claudeCommandText       = transcript.CommandText
	claudeConfigDir         = transcript.ConfigDir
	claudeFinishSubAgents   = transcript.FinishSubAgents
	claudeProjectDir        = transcript.ProjectDir
	claudePromptTitle       = transcript.PromptTitle
	claudeSubAgent          = transcript.SubAgents
	claudeSubAgentStep      = transcript.SubAgentStep
	claudeSummaryReadLimit  = transcript.SummaryReadLimit
	claudeTaskFrame         = transcript.TaskFrame
	claudeTaskReport        = transcript.TaskReport
	claudeToolCall          = transcript.ToolCall
	claudeToolOutput        = transcript.ToolOutput
	claudeToolTitle         = transcript.ToolTitle
	claudeVisibleText       = transcript.VisibleText
	clippedTitle            = transcript.ClippedTitle
	findClaudeSession       = transcript.Find
	forkClaudeSessionBefore = transcript.ForkBefore
	inputString             = transcript.InputString
	interruptClaudeAgents   = transcript.InterruptAgents
	listClaudeSessions      = transcript.List
	localImageDataURI       = transcript.LocalImageURI
	markClaudeSuperseded    = transcript.MarkSuperseded
	messageLabel            = transcript.MessageLabel
	parseClaudeSession      = transcript.Parse
	summarizeClaudeSession  = transcript.Summarize
)

// Message display helpers live in internal/msgfmt.
type (
	activityRun  = msgfmt.ActivityRun
	messageGroup = msgfmt.MessageGroup
)

var (
	activityIcon           = msgfmt.ActivityIcon
	activityRuns           = msgfmt.ActivityRuns
	attachmentDisplay      = msgfmt.AttachmentDisplay
	codexTurnTimes         = msgfmt.CodexTurnTimes
	collapsedThreadCount   = msgfmt.CollapsedThreadCount
	completeClaudeTiming   = msgfmt.CompleteClaudeTiming
	conversationTitle      = msgfmt.ConversationTitle
	displayedThreads       = msgfmt.DisplayedThreads
	formatElapsed          = msgfmt.FormatElapsed
	formatMessageTimestamp = msgfmt.FormatMessageTimestamp
	groupMessages          = msgfmt.GroupMessages
	imageSource            = msgfmt.ImageSource
	itemMessage            = msgfmt.ItemMessage
	jsonText               = msgfmt.JsonText
	messageAttachments     = msgfmt.MessageAttachments
	messageGroupText       = msgfmt.MessageGroupText
	messagePrompt          = msgfmt.MessagePrompt
	messageViewKey         = msgfmt.MessageViewKey
	nativeText             = msgfmt.NativeText
	recordClaudeOutput     = msgfmt.RecordClaudeOutput
	recordCodexOutput      = msgfmt.RecordCodexOutput
	resetTurnOutput        = msgfmt.ResetTurnOutput
	threadMessageGroups    = msgfmt.ThreadMessageGroups
	threadReconnectLabel   = msgfmt.ThreadReconnectLabel
	threadTokenRateLabel   = msgfmt.ThreadTokenRateLabel
	threadWorkingLabel     = msgfmt.ThreadWorkingLabel
	turnElapsedLabel       = msgfmt.TurnElapsedLabel
)

// Composer parsing lives in internal/composer.
type (
	composerToken = composer.ComposerToken
)

var (
	agentChoices             = composer.AgentChoices
	agentReferenceText       = composer.AgentReferenceText
	collaborationTaggedInput = composer.CollaborationTaggedInput
	commandChoices           = composer.CommandChoices
	composerCommands         = composer.ComposerCommands
	composerEditEnd          = composer.ComposerEditEnd
	composerTokenAt          = composer.ComposerTokenAt
	fileChoices              = composer.FileChoices
	fileMentionInputs        = composer.FileMentionInputs
	indexProjectFiles        = composer.IndexProjectFiles
	insertAgentReference     = composer.InsertAgentReference
	insertFileReference      = composer.InsertFileReference
	slashCommand             = composer.SlashCommand
)

// The phone server's controller-free parts live in internal/phone; the
// remote terminal client lives in internal/svcproto.
type (
	phoneThreadSync = phone.ThreadSync
	terminalFrame   = svcproto.TerminalFrame
)

const (
	maxPhoneImageBytes = phone.MaxImageBytes
	maxPhoneImageTotal = phone.MaxImageTotal
	maxPhoneImages     = phone.MaxImages
)

var (
	hasHistory          = phone.HasHistory
	listThread          = phone.ListThread
	loadPhoneEnabled    = phone.LoadEnabled
	newPhoneThreadSync  = phone.NewThreadSync
	openRemoteTerminal  = svcproto.OpenTerminal
	persistPhoneEnabled = phone.PersistEnabled
	phoneDir            = phone.Dir
	pruneOldUploads     = phone.PruneOldUploads
	readPhoneImages     = phone.ReadImages
	sameJSON            = phone.SameJSON
	savePhoneImages     = phone.SaveImages
)

// Moved to internal/prefs.
var (
	loadLocale                 = prefs.LoadLocale
	loadPermissionPolicy       = prefs.LoadPermissionPolicy
	loadSetting                = prefs.Load
	normalizeAppearance        = prefs.NormalizeAppearance
	normalizeInterfaceLanguage = prefs.NormalizeInterfaceLanguage
	normalizeLocale            = prefs.NormalizeLocale
	normalizePermissionPolicy  = prefs.NormalizePermissionPolicy
	permissionPolicyPath       = prefs.SettingsPath
	persistLocale              = prefs.SaveLocale
	persistPermissionPolicy    = prefs.SavePermissionPolicy
	persistProjectOrder        = prefs.SaveProjectOrder
	persistSetting             = prefs.Save
	restoreProjectOrder        = prefs.RestoreProjectOrder
)

// Moved to internal/prefs.
var (
	approvalForPermissionPolicy    = prefs.ApprovalForPolicy
	reviewerForPermissionPolicy    = prefs.ReviewerForPolicy
	sandboxForPermissionPolicy     = prefs.SandboxForPolicy
	turnSandboxForPermissionPolicy = prefs.TurnSandboxForPolicy
)

// Moved to internal/claude.
const (
	claudeStallAfter = claude.StallAfter
)

var (
	claudeErrorMessage   = claude.ErrorMessage
	claudeHistoryPath    = claude.HistoryPath
	claudeInput          = claude.Input
	claudeModelName      = claude.ModelName
	claudePermissionMode = claude.PermissionMode
	claudeTextKey        = claude.TextKey
	saveClaudeHistory    = claude.SaveHistory
	turnHasOutput        = claude.TurnHasOutput
)

// Moved to internal/mcpconfig.
const (
	maxDelegationDepth = mcpconfig.MaxDelegationDepth
)

var (
	claudeMCPArgs  = mcpconfig.ClaudeArgs
	claudeMCPEnv   = mcpconfig.ClaudeEnv
	codexMCPArgs   = mcpconfig.CodexArgs
	isMiyaToolCall = mcpconfig.IsMiyaToolCall
	tomlString     = mcpconfig.TomlString
)

// Moved to internal/automation.
const (
	automationAgentLabel = automation.LaunchAgentLabel
)

var (
	automationAgentPath   = automation.LaunchAgentPath
	automationAgentPlist  = automation.LaunchAgentPlist
	launchdTarget         = automation.LaunchdTarget
	removeAutomationAgent = automation.RemoveLaunchAgent
)

// Moved to internal/account.
type (
	accountCaller = account.Caller
)

var (
	executeAccountReset    = account.ExecuteReset
	newAccountResetAttempt = account.NewResetAttempt
	quotaLabel             = account.QuotaLabel
	readAccountDisplay     = account.ReadDisplay
	resetExpiryText        = account.ResetExpiryText
	resetOutcomeText       = account.ResetOutcomeText
)

// Moved to internal/model.
var (
	resetCodexSession = model.ResetCodexSession
)

// Moved to internal/threadlist.
type (
	threadListCache = threadlist.Cache
)

var (
	loadProjects         = threadlist.LoadProjects
	loadThreadListCache  = threadlist.Load
	threadListCachePath  = threadlist.CachePath
	threadMetaOf         = threadlist.MetaOf
	writeThreadListCache = threadlist.Write
)

// Moved to internal/runtime/tracker.
type (
	conversationRuntime = tracker.ConversationRuntime
	runtimeManager      = tracker.Manager
	runtimeRequest      = tracker.Request
	runtimeSnapshot     = tracker.TaskSnapshot
	runtimeSource       = tracker.SnapshotSource
)

var (
	newRuntimeManager    = tracker.NewManager
	remoteRuntimeRequest = tracker.RemoteRuntimeRequest
	runtimeKey           = tracker.KeyOf
	runtimeResult        = tracker.Result
)

// Moved to internal/phone.
type (
	desktopFileServer = phone.DesktopFileServer
	fileProject       = phone.FileProject
)

var (
	projectFileID = phone.ProjectFileID
)

// Moved to internal/svcproto.
type (
	hostCommands = svcproto.HostCommands
	hostEntry    = svcproto.HostEntry
	hostRequest  = svcproto.HostRequest
	hostSides    = svcproto.HostSides
)

var (
	hostProjectPath = svcproto.HostProjectPath
)

// Moved to internal/phone.
var (
	readDesktopUploads = phone.ReadDesktopUploads
	saveDesktopUploads = phone.SaveDesktopUploads
)

// Moved to internal/workspaces.
type (
	desktopWorkspace      = workspaces.Workspace
	desktopWorkspaceStore = workspaces.Store
)

const (
	localDesktopWorkspace = workspaces.Local
)

var (
	cacheName                   = workspaces.CacheName
	connectRemoteService        = workspaces.ConnectRemoteService
	connectRemoteServiceContext = workspaces.ConnectRemoteServiceContext
	desktopID                   = workspaces.IDFromLink
	loadDesktopWorkspaces       = workspaces.Load
	remoteServerLink            = workspaces.RemoteServerLink
	remoteServerPath            = workspaces.RemoteServerPath
	saveRemoteServer            = workspaces.SaveRemoteServer
)

// Moved to internal/codex.
var (
	approvalResult = codex.ApprovalResult
	isMiyaApproval = codex.IsMiyaApproval
	validateForm   = codex.ValidateForm
)

// Moved to internal/phoneapi.
var (
	approvalFingerprint = phoneapi.ApprovalFingerprint
	validateMobileForm  = phoneapi.ValidateMobileForm
)

// Moved to internal/msgfmt.
var (
	changedFiles  = msgfmt.ChangedFiles
	lastReply     = msgfmt.LastReply
	packetSection = msgfmt.PacketSection
	threadRefTask = msgfmt.ThreadRefTask
	threadRefs    = msgfmt.ThreadRefs
)

// Moved to internal/composer.
var (
	insertThreadReference = composer.InsertThreadReference
)

// Moved to internal/delegation/coordinator.
var (
	codexFinalPhase      = coordinator.CodexFinalPhase
	managedLongRunning   = coordinator.LongRunning
	managedResult        = coordinator.NativeResult
	provenCodexRejection = coordinator.ProvenCodexRejection
)

// Moved to internal/msgfmt.
const (
	retryContinuePrompt = msgfmt.RetryContinuePrompt
)

var (
	failedAfterOutput = msgfmt.FailedAfterOutput
	retryableError    = msgfmt.RetryableError
)

// Moved to internal/model.
var (
	activeThreadGoal = model.ActiveGoal
	applyCodexGoal   = model.ApplyCodexGoal
	setThreadTask    = model.SetThreadTask
)
