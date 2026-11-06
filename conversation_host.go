package main

import "miya/internal/codex"

func (c *chatController) Current() Thread             { return c.current() }
func (c *chatController) Revision() int               { return c.revision() }
func (c *chatController) Approvals() []codex.Approval { return c.approvals() }
func (c *chatController) SelectThread(t Thread)       { c.selectThread(t) }
func (v *mobileViewer) Current() Thread               { return v.current() }
func (v *mobileViewer) Revision() int                 { return v.revision() }
func (v *mobileViewer) Approvals() []codex.Approval   { return v.approvals() }
func (v *mobileViewer) SelectThread(t Thread)         { v.selectThread(t) }
