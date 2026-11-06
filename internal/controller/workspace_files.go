package controller

import (
	projectaccess "miya/internal/project/access"
	svcclient "miya/internal/service/client"
)

// workspaceFiles reads a project's files and changes and opens its
// terminals: on this Mac, or on the server for a remote server workspace.
// Its methods may run off the controller's thread.
type workspaceFiles = projectaccess.Files

// workspaceFiles is the open workspace's files. Call it on the controller's
// thread, as the workspace it picks can change there.
func (c *Controller) WorkspaceFiles() workspaceFiles {
	if c.RemoteHost() {
		return svcclient.Files{Client: c.Remote}
	}
	return projectaccess.Local{}
}
