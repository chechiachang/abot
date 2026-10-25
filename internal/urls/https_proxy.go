package urls

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

func (r *Reader) startHTTPSProxy(ctx context.Context) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, errors.New("start restricted download proxy")
	}
	proxyCtx, cancel := context.WithCancel(ctx)
	semaphore := make(chan struct{}, 24)
	budget := &transferBudget{limit: r.config.MaxBytes}
	var requestCount atomic.Int64
	go func() {
		defer listener.Close()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			if requestCount.Add(1) > 100 {
				_, _ = io.WriteString(connection, "HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\n\r\n")
				_ = connection.Close()
				continue
			}
			select {
			case semaphore <- struct{}{}:
				go func() {
					defer func() { <-semaphore }()
					r.handleConnectBudget(proxyCtx, connection, budget)
				}()
			case <-proxyCtx.Done():
				_ = connection.Close()
				return
			default:
				_, _ = io.WriteString(connection, "HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\n\r\n")
				_ = connection.Close()
			}
		}
	}()
	address := listener.Addr().String()
	cleanup := func() {
		cancel()
		_ = listener.Close()
	}
	return "http://" + address, cleanup, nil
}

func (r *Reader) handleConnect(ctx context.Context, client net.Conn) {
	r.handleConnectBudget(ctx, client, &transferBudget{limit: r.config.MaxBytes})
}

func (r *Reader) handleConnectBudget(ctx context.Context, client net.Conn, budget *transferBudget) {
	defer client.Close()
	reader := bufio.NewReader(client)
	request, err := http.ReadRequest(reader)
	if err != nil || request.Method != http.MethodConnect {
		_, _ = io.WriteString(client, "HTTP/1.1 403 Forbidden\r\nConnection: close\r\n\r\n")
		return
	}
	host, port, err := net.SplitHostPort(request.Host)
	if err != nil {
		host, port = request.Host, "443"
		if len(host) > 2 && host[0] == '[' && host[len(host)-1] == ']' {
			host = host[1 : len(host)-1]
		}
	}
	if port != "443" || host == "" {
		_, _ = io.WriteString(client, "HTTP/1.1 403 Forbidden\r\nConnection: close\r\n\r\n")
		return
	}
	remote, err := r.dialPublic(ctx, "tcp", host, port)
	if err != nil {
		_, _ = io.WriteString(client, "HTTP/1.1 403 Forbidden\r\nConnection: close\r\n\r\n")
		return
	}
	defer remote.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	copyDone := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, budgetReader{Reader: reader, budget: budget}); copyDone <- struct{}{} }()
	go func() { _, _ = io.Copy(client, budgetReader{Reader: remote, budget: budget}); copyDone <- struct{}{} }()
	select {
	case <-ctx.Done():
	case <-copyDone:
	}
}

type transferBudget struct {
	limit int64
	used  atomic.Int64
}

type budgetReader struct {
	Reader io.Reader
	budget *transferBudget
}

func (r budgetReader) Read(buffer []byte) (int, error) {
	n, err := r.Reader.Read(buffer)
	if n > 0 && r.budget.limit > 0 && r.budget.used.Add(int64(n)) > r.budget.limit {
		return 0, errors.New("proxy transfer limit exceeded")
	}
	return n, err
}

func (r *Reader) dialPublic(ctx context.Context, network, host, port string) (net.Conn, error) {
	ips, err := r.lookupPublic(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range ips {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
