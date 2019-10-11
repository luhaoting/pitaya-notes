// Copyright (c) nano Author and TFG Co. All Rights Reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package acceptor

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topfreegames/pitaya/constants"
	"github.com/topfreegames/pitaya/logger"
)

// WSAcceptor struct
type WSAcceptor struct {
	addr     string
	connChan chan net.Conn
	listener net.Listener
	certFile string
	keyFile  string
}

// NewWSAcceptor returns a new instance of WSAcceptor
func NewWSAcceptor(addr string, certs ...string) *WSAcceptor {
	keyFile := ""
	certFile := ""
	if len(certs) != 2 && len(certs) != 0 {
		panic(constants.ErrInvalidCertificates)
	} else if len(certs) == 2 {
		certFile = certs[0]
		keyFile = certs[1]
	}

	w := &WSAcceptor{
		addr:     addr,
		connChan: make(chan net.Conn),
		certFile: certFile,
		keyFile:  keyFile,
	}
	return w
}

// GetAddr returns the addr the acceptor will listen on
func (w *WSAcceptor) GetAddr() string {
	if w.listener != nil {
		return w.listener.Addr().String()
	}
	return ""
}

// GetConnChan gets a connection channel
func (w *WSAcceptor) GetConnChan() chan net.Conn {
	return w.connChan
}

//http.Serve(w.listener, w)启动http时候的第二个参数connHandler 需要实现Handler接口即ServeHTTP函数
type connHandler struct {
	upgrader *websocket.Upgrader
	connChan chan net.Conn
}

//Hander接口 处理http请求
func (h *connHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(rw, r, nil) //升级http为websocket
	if err != nil {
		logger.Log.Errorf("Upgrade failure, URI=%s, Error=%s", r.RequestURI, err.Error())
		return
	}

	c, err := newWSConn(conn) //创建websocket conn 并且放入chan
	if err != nil {
		logger.Log.Errorf("Failed to create new ws connection: %s", err.Error())
		return
	}
	h.connChan <- c
}

func (w *WSAcceptor) hasTLSCertificates() bool {
	return w.certFile != "" && w.keyFile != ""
}

// ListenAndServe listens and serve in the specified addr
func (w *WSAcceptor) ListenAndServe() {
	if w.hasTLSCertificates() {
		w.ListenAndServeTLS(w.certFile, w.keyFile)
		return
	}

	//升级http请求 io操作缓存大小
	var upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true //请求检查函数 防止跨站请求伪造 true则不检查
		},
	}

	listener, err := net.Listen("tcp", w.addr)
	if err != nil {
		logger.Log.Fatalf("Failed to listen: %s", err.Error())
	}
	w.listener = listener

	w.serve(&upgrader)
}

// ListenAndServeTLS listens and serve in the specified addr using tls
func (w *WSAcceptor) ListenAndServeTLS(cert, key string) {
	var upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	crt, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		logger.Log.Fatalf("Failed to load x509: %s", err.Error())
	}

	tlsCfg := &tls.Config{Certificates: []tls.Certificate{crt}}
	listener, err := tls.Listen("tcp", w.addr, tlsCfg)
	if err != nil {
		logger.Log.Fatalf("Failed to listen: %s", err.Error())
	}
	w.listener = listener
	w.serve(&upgrader)
}

func (w *WSAcceptor) serve(upgrader *websocket.Upgrader) {
	defer w.Stop()

	//启动http服务 connHandler需要实现Handler接口即ServeHTTP函数
	// w.listener监听接收每一个http连接并且开启一个goroutine来处理连接，goroutine使用connHandler处理连接请求并且回复
	http.Serve(w.listener, &connHandler{
		upgrader: upgrader,
		connChan: w.connChan,
	})
}

// Stop stops the acceptor
func (w *WSAcceptor) Stop() {
	err := w.listener.Close()
	if err != nil {
		logger.Log.Errorf("Failed to stop: %s", err.Error())
	}
}

// wsConn is an adapter to t.Conn, which implements all t.Conn
// interface base on *websocket.Conn
type wsConn struct {
	conn   *websocket.Conn
	typ    int // message type
	reader io.Reader
}

// newWSConn return an initialized *wsConn
func newWSConn(conn *websocket.Conn) (*wsConn, error) {
	c := &wsConn{conn: conn}

	t, r, err := conn.NextReader() //返回连接上的消息类型（二进制或者为本）以及一个打开的reader
	if err != nil {
		return nil, err
	}

	c.typ = t
	c.reader = r

	return c, nil
}

// Read reads data from the connection.
// Read can be made to time out and return an Error with Timeout() == true
// after a fixed time limit; see SetDeadline and SetReadDeadline.
func (c *wsConn) Read(b []byte) (int, error) {
	n, err := c.reader.Read(b)
	if err != nil && err != io.EOF {
		return n, err
	} else if err == io.EOF {
		_, r, err := c.conn.NextReader()
		if err != nil {
			return 0, err
		}
		c.reader = r
	}

	return n, nil
}

// Write writes data to the connection.
// Write can be made to time out and return an Error with Timeout() == true
// after a fixed time limit; see SetDeadline and SetWriteDeadline.
func (c *wsConn) Write(b []byte) (int, error) {
	err := c.conn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}

	return len(b), nil
}

// Close closes the connection.
// Any blocked Read or Write operations will be unblocked and return errors.
func (c *wsConn) Close() error {
	return c.conn.Close()
}

// LocalAddr returns the local network address.
func (c *wsConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// RemoteAddr returns the remote network address.
func (c *wsConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// SetDeadline sets the read and write deadlines associated
// with the connection. It is equivalent to calling both
// SetReadDeadline and SetWriteDeadline.
//
// A deadline is an absolute time after which I/O operations
// fail with a timeout (see type Error) instead of
// blocking. The deadline applies to all future and pending
// I/O, not just the immediately following call to Read or
// Write. After a deadline has been exceeded, the connection
// can be refreshed by setting a deadline in the future.
//
// An idle timeout can be implemented by repeatedly extending
// the deadline after successful Read or Write calls.
//
// A zero value for t means I/O operations will not time out.
func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}

	return c.SetWriteDeadline(t)
}

// SetReadDeadline sets the deadline for future Read calls
// and any currently-blocked Read call.
// A zero value for t means Read will not time out.
func (c *wsConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the deadline for future Write calls
// and any currently-blocked Write call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means Write will not time out.
func (c *wsConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
