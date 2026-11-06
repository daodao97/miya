package appmodel

import (
	account "miya/internal/account"
	agentusage "miya/internal/agentusage"
	async "miya/internal/async"
	automation "miya/internal/automation"
	claude "miya/internal/claude"
	transcript "miya/internal/claude/transcript"
	codex "miya/internal/codex"
	composer "miya/internal/composer"
	coordinator "miya/internal/delegation/coordinator"
	fsutil "miya/internal/fsutil"
	mcpconfig "miya/internal/mcpconfig"
	mcpproto "miya/internal/mcpproto"
	mcpserver "miya/internal/mcpserver"
	model "miya/internal/model"
	msgfmt "miya/internal/msgfmt"
	phone "miya/internal/phone"
	phoneapi "miya/internal/phoneapi"
	phonecache "miya/internal/phonecache"
	prefs "miya/internal/prefs"
	tracker "miya/internal/runtime/tracker"
	svcproto "miya/internal/svcproto"
	threadlist "miya/internal/threadlist"
	workspaces "miya/internal/workspaces"
)

// The conversation data lives in internal/model. These aliases keep
// package main's code unchanged while it is split into packages; new
// packages use the model names directly.
type Message = model.Message

type Thread = model.Thread

type MessagePart = model.MessagePart

type ThreadRef = model.ThreadRef

type SubAgent = model.SubAgent

type ThreadTask = model.ThreadTask

type DiffLineStats = model.DiffLineStats

type MemoryNotice = model.MemoryNotice

type QueuedMessage = model.QueuedMessage

type SendRequest = model.SendRequest

type Project = model.Project

type CollaborationStatus = model.CollaborationStatus

type ComposerChoice = model.ComposerChoice

type composerMention = model.Mention

var SourceIDPattern = model.SourceIDPattern

var ComposerMentions = model.Mentions

var MessageParts = model.MessageParts

// The phone protocol's data lives in internal/phoneapi.
type PhoneState = phoneapi.State

type PhoneProject = phoneapi.Project

type PhoneThreadDelta = phoneapi.ThreadDelta

type PhoneApprovalReply = phoneapi.ApprovalReply

type phoneFormField = phoneapi.FormField

type PhoneFormSchema = phoneapi.FormSchema

type PhoneModelOption = phoneapi.ModelOption

type PhoneAgentOptions = phoneapi.AgentOptions

type PhoneAgentQuota = phoneapi.AgentQuota

type PhoneQuotaWindow = phoneapi.QuotaWindow

type PhoneMemoryResult = phoneapi.MemoryResult

type PhoneAutomationRunResult = phoneapi.AutomationRunResult

type PhoneAutomationFormResult = phoneapi.AutomationFormResult

type PhoneDesktopReply = phoneapi.DesktopReply

type PhoneCollaborationResult = phoneapi.CollaborationResult

type PhoneCommands = phoneapi.Commands

// The account data lives in internal/account.
type AccountDisplay = account.Display

type AccountLimits = account.Limits

type AccountIdentity = account.Identity

type QuotaSnapshot = account.QuotaSnapshot

type QuotaWindow = account.QuotaWindow

type ResetCredit = account.ResetCredit

type resetCreditsSummary = account.ResetCreditsSummary

type accountResetAttempt = account.ResetAttempt

type AccountResetAction = account.ResetAction

var sameAccount = account.SameAccount

var NextResetCredit = account.NextResetCredit

var FormatCreditBalance = account.FormatBalance

// The service protocol lives in internal/svcproto.
type SvcRequest = svcproto.Request

type SvcMessages = svcproto.ThreadMessages

type SvcMessage = svcproto.Envelope

type SvcState = svcproto.State

type SvcDraft = svcproto.Draft

type SvcHello = svcproto.Hello

type SvcThreadValue = svcproto.ThreadValue

type SvcTaskAction = svcproto.TaskAction

type SvcRespond = svcproto.Respond

type SvcQueued = svcproto.Queued

type SvcRewind = svcproto.Rewind

type SvcCommandResult = svcproto.CommandResult

type SvcCommand = svcproto.Command

type SvcProject = svcproto.ProjectRequest

type SvcWorkspace = svcproto.WorkspaceResult

type PhoneStatus = svcproto.PhoneStatus

const ServiceVersion = svcproto.Version

var ServiceSocket = svcproto.Socket

var MessageFingerprint = svcproto.MessageFingerprint

// A window's connection to the service lives in internal/svcproto too.
type ServiceClient = svcproto.Client

type ServiceInbox = svcproto.Inbox

const MiyaRequestTimeout = svcproto.CardTimeout

var NewServiceClient = svcproto.NewClient

var newWireServiceClient = svcproto.NewWireClient

var NewDetachedServiceClient = svcproto.NewDetachedClient

var NewClosedServiceClient = svcproto.NewClosedClient

var DialService = svcproto.Dial

var ErrServiceClosed = svcproto.ErrClosed

var ServiceCallTimeout = svcproto.CallTimeout

// deferred is a reply worked out off the main thread; see async.Reply.
type Deferred = async.Reply

// The MCP tool-call data lives in internal/mcpproto.
type McpTool = mcpproto.Tool

type McpRequest = mcpproto.Request

type McpThreadInfo = mcpproto.ThreadInfo

type McpThreadsResult = mcpproto.ThreadsResult

type McpReadResult = mcpproto.ReadResult

type McpStatusResult = mcpproto.StatusResult

type McpDelegateResult = mcpproto.DelegateResult

type mcpError = mcpproto.Error

type McpAutomationArgs = mcpproto.AutomationArgs

const McpCallerUnknown = mcpproto.CallerUnknown

var McpReadOnly = mcpproto.ReadOnly

// The -mcp process's server lives in internal/mcpserver.
type McpServer = mcpserver.Server

var NewMCPServer = mcpserver.NewServer

var ServeMCP = mcpserver.Serve

var RegisterMCPTools = mcpserver.Register

// mcpTools are the MCP tools as registered so far; call it after init.
func McpTools() []McpTool { return mcpserver.Tools() }

// Claude Code transcripts are read by internal/claude/transcript.
type (
	ClaudeBlock = transcript.Block
)

var ApplyClaudeAgentTasks = transcript.ApplyAgentTasks

var ClaudeBlocks = transcript.Blocks

var ClaudeCommandText = transcript.CommandText

var ClaudeConfigDir = transcript.ConfigDir

var ClaudeFinishSubAgents = transcript.FinishSubAgents

var ClaudeProjectDir = transcript.ProjectDir

var ClaudePromptTitle = transcript.PromptTitle

var ClaudeSubAgent = transcript.SubAgents

var ClaudeSubAgentStep = transcript.SubAgentStep

var ClaudeSummaryReadLimit = transcript.SummaryReadLimit

var ClaudeTaskFrame = transcript.TaskFrame

var ClaudeTaskReport = transcript.TaskReport

var ClaudeToolCall = transcript.ToolCall

var ClaudeToolOutput = transcript.ToolOutput

var ClaudeToolTitle = transcript.ToolTitle

var ClaudeVisibleText = transcript.VisibleText

var ClippedTitle = transcript.ClippedTitle

var FindClaudeSession = transcript.Find

var ForkClaudeSessionBefore = transcript.ForkBefore

var inputString = transcript.InputString

var InterruptClaudeAgents = transcript.InterruptAgents

var ListClaudeSessions = transcript.List

var LocalImageDataURI = transcript.LocalImageURI

var MarkClaudeSuperseded = transcript.MarkSuperseded

var MessageLabel = transcript.MessageLabel

var ParseClaudeSession = transcript.Parse

var SummarizeClaudeSession = transcript.Summarize

// Message display helpers live in internal/msgfmt.
type ActivityRun = msgfmt.ActivityRun

type MessageGroup = msgfmt.MessageGroup

var ActivityIcon = msgfmt.ActivityIcon

var ActivityRuns = msgfmt.ActivityRuns

var AttachmentDisplay = msgfmt.AttachmentDisplay

var CodexTurnTimes = msgfmt.CodexTurnTimes

var CollapsedThreadCount = msgfmt.CollapsedThreadCount

var CompleteClaudeTiming = msgfmt.CompleteClaudeTiming

var ConversationTitle = msgfmt.ConversationTitle

var DisplayedThreads = msgfmt.DisplayedThreads

var FormatElapsed = msgfmt.FormatElapsed

var FormatMessageTimestamp = msgfmt.FormatMessageTimestamp

var GroupMessages = msgfmt.GroupMessages

var ImageSource = msgfmt.ImageSource

var ItemMessage = msgfmt.ItemMessage

var JsonText = msgfmt.JsonText

var MessageAttachments = msgfmt.MessageAttachments

var MessageGroupText = msgfmt.MessageGroupText

var MessagePrompt = msgfmt.MessagePrompt

var MessageViewKey = msgfmt.MessageViewKey

var NativeText = msgfmt.NativeText

var RecordClaudeOutput = msgfmt.RecordClaudeOutput

var RecordCodexOutput = msgfmt.RecordCodexOutput

var ResetTurnOutput = msgfmt.ResetTurnOutput

var ThreadMessageGroups = msgfmt.ThreadMessageGroups

var ThreadReconnectLabel = msgfmt.ThreadReconnectLabel

var ThreadTokenRateLabel = msgfmt.ThreadTokenRateLabel

var ThreadWorkingLabel = msgfmt.ThreadWorkingLabel

var TurnElapsedLabel = msgfmt.TurnElapsedLabel

// Composer parsing lives in internal/composer.
type (
	ComposerToken = composer.ComposerToken
)

var AgentChoices = composer.AgentChoices

var AgentReferenceText = composer.AgentReferenceText

var CollaborationTaggedInput = composer.CollaborationTaggedInput

var CommandChoices = composer.CommandChoices

var ComposerCommands = composer.ComposerCommands

var ComposerEditEnd = composer.ComposerEditEnd

var ComposerTokenAt = composer.ComposerTokenAt

var FileChoices = composer.FileChoices

var FileMentionInputs = composer.FileMentionInputs

var IndexProjectFiles = composer.IndexProjectFiles

var InsertAgentReference = composer.InsertAgentReference

var InsertFileReference = composer.InsertFileReference

var SlashCommand = composer.SlashCommand

// The phone server's controller-free parts live in internal/phone; the
// remote terminal client lives in internal/svcproto.
type PhoneThreadSync = phone.ThreadSync

type TerminalFrame = svcproto.TerminalFrame

const (
	maxPhoneImageBytes = phone.MaxImageBytes
	maxPhoneImageTotal = phone.MaxImageTotal
	MaxPhoneImages     = phone.MaxImages
)

var HasHistory = phone.HasHistory

var ListThread = phone.ListThread

var LoadPhoneEnabled = phone.LoadEnabled

var NewPhoneThreadSync = phone.NewThreadSync

var OpenRemoteTerminal = svcproto.OpenTerminal

var persistPhoneEnabled = phone.PersistEnabled

var phoneDir = phone.Dir

var PruneOldUploads = phone.PruneOldUploads

var ReadPhoneImages = phone.ReadImages

var SameJSON = phone.SameJSON

var SavePhoneImages = phone.SaveImages

// Moved to internal/prefs.
var LoadLocale = prefs.LoadLocale

var LoadPermissionPolicy = prefs.LoadPermissionPolicy

var LoadSetting = prefs.Load

var NormalizeAppearance = prefs.NormalizeAppearance

var NormalizeInterfaceLanguage = prefs.NormalizeInterfaceLanguage

var NormalizeLocale = prefs.NormalizeLocale

var NormalizePermissionPolicy = prefs.NormalizePermissionPolicy

var PermissionPolicyPath = prefs.SettingsPath

var PersistLocale = prefs.SaveLocale

var PersistPermissionPolicy = prefs.SavePermissionPolicy

var PersistProjectOrder = prefs.SaveProjectOrder

var PersistSetting = prefs.Save

var RestoreProjectOrder = prefs.RestoreProjectOrder

// Moved to internal/prefs.
var ApprovalForPermissionPolicy = prefs.ApprovalForPolicy

var ReviewerForPermissionPolicy = prefs.ReviewerForPolicy

var SandboxForPermissionPolicy = prefs.SandboxForPolicy

var TurnSandboxForPermissionPolicy = prefs.TurnSandboxForPolicy

// Moved to internal/claude.
const (
	claudeStallAfter = claude.StallAfter
)

var ClaudeErrorMessage = claude.ErrorMessage

var ClaudeHistoryPath = claude.HistoryPath

var ClaudeInput = claude.Input

var ClaudeModelName = claude.ModelName

var ClaudePermissionMode = claude.PermissionMode

var ClaudeTextKey = claude.TextKey

var SaveClaudeHistory = claude.SaveHistory

var TurnHasOutput = claude.TurnHasOutput

// Moved to internal/mcpconfig.
const (
	MaxDelegationDepth = mcpconfig.MaxDelegationDepth
)

var ClaudeMCPArgs = mcpconfig.ClaudeArgs

var ClaudeMCPEnv = mcpconfig.ClaudeEnv

var CodexMCPArgs = mcpconfig.CodexArgs

var IsMiyaToolCall = mcpconfig.IsMiyaToolCall

var TomlString = mcpconfig.TomlString

// Moved to internal/automation.
const (
	AutomationAgentLabel = automation.LaunchAgentLabel
)

var AutomationAgentPath = automation.LaunchAgentPath

var AutomationAgentPlist = automation.LaunchAgentPlist

var LaunchdTarget = automation.LaunchdTarget

var RemoveAutomationAgent = automation.RemoveLaunchAgent

// Moved to internal/account.
type (
	AccountCaller = account.Caller
)

var ExecuteAccountReset = account.ExecuteReset

var NewAccountResetAttempt = account.NewResetAttempt

var QuotaLabel = account.QuotaLabel

var ReadAccountDisplay = account.ReadDisplay

var ResetExpiryText = account.ResetExpiryText

var ResetOutcomeText = account.ResetOutcomeText

// Moved to internal/model.
var (
	ResetCodexSession = model.ResetCodexSession
)

// Moved to internal/threadlist.
type (
	ThreadListCache = threadlist.Cache
)

var LoadProjects = threadlist.LoadProjects

var LoadThreadListCache = threadlist.Load

var ThreadListCachePath = threadlist.CachePath

var ThreadMetaOf = threadlist.MetaOf

var WriteThreadListCache = threadlist.Write

// Moved to internal/runtime/tracker.
type ConversationRuntime = tracker.ConversationRuntime

type RuntimeManager = tracker.Manager

type RuntimeRequest = tracker.Request

type RuntimeSnapshot = tracker.TaskSnapshot

type RuntimeSource = tracker.SnapshotSource

var NewRuntimeManager = tracker.NewManager

var RemoteRuntimeRequest = tracker.RemoteRuntimeRequest

var RuntimeKey = tracker.KeyOf

var RuntimeResult = tracker.Result

// Moved to internal/phone.
type DesktopFileServer = phone.DesktopFileServer

type FileProject = phone.FileProject

var (
	ProjectFileID = phone.ProjectFileID
)

// Moved to internal/svcproto.
type HostCommands = svcproto.HostCommands

type HostEntry = svcproto.HostEntry

type HostRequest = svcproto.HostRequest

type HostSides = svcproto.HostSides

var (
	HostProjectPath = svcproto.HostProjectPath
)

// Moved to internal/phone.
var ReadDesktopUploads = phone.ReadDesktopUploads

var SaveDesktopUploads = phone.SaveDesktopUploads

// Moved to internal/workspaces.
type DesktopWorkspace = workspaces.Workspace

type DesktopWorkspaceStore = workspaces.Store

const (
	LocalDesktopWorkspace = workspaces.Local
)

var CacheName = workspaces.CacheName

var ConnectRemoteService = workspaces.ConnectRemoteService

var ConnectRemoteServiceContext = workspaces.ConnectRemoteServiceContext

var DesktopID = workspaces.IDFromLink

var LoadDesktopWorkspaces = workspaces.Load

var RemoteServerLink = workspaces.RemoteServerLink

var RemoteServerPath = workspaces.RemoteServerPath

var SaveRemoteServer = workspaces.SaveRemoteServer

// Moved to internal/codex.
var ApprovalResult = codex.ApprovalResult

var IsMiyaApproval = codex.IsMiyaApproval

var ValidateForm = codex.ValidateForm

// Moved to internal/phoneapi.
var ApprovalFingerprint = phoneapi.ApprovalFingerprint

var ValidateMobileForm = phoneapi.ValidateMobileForm

// Moved to internal/msgfmt.
var ChangedFiles = msgfmt.ChangedFiles

var LastReply = msgfmt.LastReply

var PacketSection = msgfmt.PacketSection

var ThreadRefTask = msgfmt.ThreadRefTask

var ThreadRefs = msgfmt.ThreadRefs

// Moved to internal/composer.
var (
	InsertThreadReference = composer.InsertThreadReference
)

// Moved to internal/delegation/coordinator.
var CodexFinalPhase = coordinator.CodexFinalPhase

var ManagedLongRunning = coordinator.LongRunning

var ManagedResult = coordinator.NativeResult

var ProvenCodexRejection = coordinator.ProvenCodexRejection

// Moved to internal/msgfmt.
const (
	RetryContinuePrompt = msgfmt.RetryContinuePrompt
)

var FailedAfterOutput = msgfmt.FailedAfterOutput

var RetryableError = msgfmt.RetryableError

// Moved to internal/model.
var ActiveThreadGoal = model.ActiveGoal

var ApplyCodexGoal = model.ApplyCodexGoal

var SetThreadTask = model.SetThreadTask

// Moved to internal/composer.
var (
	PartsText = composer.PartsText
)

// Moved to internal/fsutil.
var ReadGzipJSON = fsutil.ReadGzipJSON

var WriteGzipJSON = fsutil.WriteGzipJSON

// Moved to internal/codex.
var (
	ParseGoalCommand = codex.ParseGoalCommand
)

// Moved to internal/agentusage.
var RemainingUsage = agentusage.RemainingUsage

var SelectedUsage = agentusage.SelectedUsage

var UsageAgentName = agentusage.AgentName

var UsageTrayTitle = agentusage.TrayTitle

// Moved to internal/msgfmt.
var (
	LocalFileLink = msgfmt.LocalFileLink
)

// Moved to internal/mcpconfig.
var McpAccessAccepted = mcpconfig.AccessAccepted

var McpSessionPermissionKey = mcpconfig.SessionPermissionKey

var RememberableMCPAccess = mcpconfig.RememberableMCPAccess

// Moved to internal/phonecache.
type (
	PhoneSavedState = phonecache.SavedState
)

const (
	CacheFlushDelay  = phonecache.CacheFlushDelay
	PhoneDiskThreads = phonecache.PhoneDiskThreads
	StateFile        = phonecache.StateFile
	StateKey         = phonecache.StateKey
)

var (
	PruneCache = phonecache.Prune
)

// Moved to internal/phonecache.
type MobileDesktop = phonecache.Desktop

type MobileDesktopSession = phonecache.Session

type MobileSecretStore = phonecache.SecretStore

const (
	MobileDesktopsKey = phonecache.DesktopsKey
)

var (
	MigrateDesktopOptions = phonecache.MigrateOptions
)

// Moved to internal/msgfmt.
var (
	PrepareHistoryMessages = msgfmt.HistoryMessages
)

// Moved to internal/codex.
var (
	IsWriterConflict = codex.IsWriterConflict
)

// Moved to internal/claude.
var ApplyClaudeSchedules = claude.ApplySchedules

var StopClaudeTasks = claude.StopTasks

// Moved to internal/model.
var LoadReferenceSource = model.LoadReferenceSource

var ReferenceSourceForService = model.ReferenceSourceForService

// Moved to internal/prefs.
var (
	PersistClaudeCommand = prefs.SaveClaudeCommand
)

// Moved to internal/fsutil.
var (
	AtomicWriteFile = fsutil.AtomicWriteFile
)

// Moved to internal/codex.
var (
	IsMissingCodexThread = codex.IsMissingThread
)

// Moved to internal/agentusage.
var (
	UsageTimeProgress = agentusage.TimeProgress
)
