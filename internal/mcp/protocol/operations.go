package protocol

var operations = map[string]bool{
	"mcpDesktopControl": true, "mcpMemory": false, "mcpThreads": false, "mcpRead": false,
	"mcpStatus": false, "mcpDelegate": false, "mcpLegacyTask": false, "mcpCollaboration": false,
	"mcpAutomationList": false, "mcpAutomationCreate": true, "mcpAutomationUpdate": true, "mcpAutomationWake": true,
	"mcpReportResult": false, "mcpRuntime": false,
}

func IsOperation(name string) bool  { _, ok := operations[name]; return ok }
func WaitsForUser(name string) bool { return operations[name] }
