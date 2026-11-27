package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	phoneproto "miya/internal/phone/protocol"
	"miya/internal/remote"
)

// OpenPaired opens the same ordinary lease as the phone's Remote desktop
// menu. A dedicated stream lets the viewer revoke even an unanswered request
// by disconnecting, without interrupting projects or conversations.
func OpenPaired(ctx context.Context, workspace *remote.Conn) (*Client, <-chan struct{}, error) {
	openCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stream, err := workspace.DialPhone(openCtx)
	if err != nil {
		return nil, nil, err
	}
	stopViewer := context.AfterFunc(ctx, func() { _ = stream.Close() })
	stopWorkspace := context.AfterFunc(workspace.Context(), func() { _ = stream.Close() })
	closeStream := func() {
		stopViewer()
		stopWorkspace()
		_ = stream.Close()
	}
	stopOpen := context.AfterFunc(openCtx, func() { _ = stream.Close() })
	token, err := openPairedLease(stream)
	stopOpen()
	if openCtx.Err() != nil {
		err = openCtx.Err()
	}
	if err != nil {
		closeStream()
		return nil, nil, err
	}
	client := NewClient(workspace.DialFiles, token)
	go func() {
		defer client.CloseIdleConnections()
		defer closeStream()
		for {
			_ = stream.SetReadDeadline(time.Now().Add(45 * time.Second))
			var frame remote.Frame
			if stream.Receive(&frame) != nil {
				return
			}
		}
	}()
	return client, stream.Context().Done(), nil
}

func openPairedLease(stream *remote.Conn) (string, error) {
	var hello remote.Frame
	if err := stream.Receive(&hello); err != nil {
		return "", err
	}
	if hello.Type != "hello" || hello.Version != remote.ProtocolVersion {
		return "", errors.New(tr("Invalid remote desktop connection"))
	}
	stream.DecompressReceives()
	id := rand.Text()
	if err := stream.Send(remote.Request{Op: "desktop-open", WindowPick: true, RequestID: id}); err != nil {
		return "", err
	}
	for {
		var frame remote.Frame
		if err := stream.Receive(&frame); err != nil {
			return "", err
		}
		if frame.Type != "desktop-open" {
			continue
		}
		var reply phoneproto.DesktopReply
		if json.Unmarshal(frame.Data, &reply) != nil || reply.RequestID != id {
			return "", errors.New(tr("Invalid remote desktop connection"))
		}
		if frame.Error != "" {
			return "", errors.New(frame.Error)
		}
		if token, err := hex.DecodeString(reply.Token); err != nil || len(token) != 32 {
			return "", errors.New(tr("Invalid remote desktop connection"))
		}
		return reply.Token, nil
	}
}
