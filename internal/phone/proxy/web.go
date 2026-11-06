package proxy

import (
	"context"
	"errors"
	"net"
	"strconv"

	"miya/internal/remote"
	"miya/internal/webservice"
)

func NewPhoneWebProxy(conn *remote.Conn, port int, host string, tokens ...string) (*PhoneFileProxy, error) {
	value := strconv.Itoa(port)
	if _, err := webservice.ParsePort(value); err != nil {
		return nil, err
	}
	token := ""
	if len(tokens) > 0 {
		token = tokens[0]
	}
	if !webservice.ValidHost(host, token) {
		return nil, errors.New(tr("Invalid local service address"))
	}
	p := new(PhoneFileProxy)
	p.WebHost = host
	if len(tokens) > 0 {
		p.webToken = tokens[0]
	}
	p.conn.Store(conn)
	err := p.StartProxy(webservice.Prefix+value, "http://localhost:"+value, func(ctx context.Context) (net.Conn, error) {
		current := p.conn.Load()
		if current == nil {
			return nil, errors.New(tr("Desktop connection lost"))
		}
		return current.DialFiles(ctx)
	})
	return p, err
}
