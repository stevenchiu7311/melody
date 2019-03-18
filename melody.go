package melody

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"runtime"
	"sync"
	"unsafe"

	"github.com/gorilla/websocket"
)

// Close codes defined in RFC 6455, section 11.7.
// Duplicate of codes from gorilla/websocket for convenience.
const (
	CloseNormalClosure           = 1000
	CloseGoingAway               = 1001
	CloseProtocolError           = 1002
	CloseUnsupportedData         = 1003
	CloseNoStatusReceived        = 1005
	CloseAbnormalClosure         = 1006
	CloseInvalidFramePayloadData = 1007
	ClosePolicyViolation         = 1008
	CloseMessageTooBig           = 1009
	CloseMandatoryExtension      = 1010
	CloseInternalServerErr       = 1011
	CloseServiceRestart          = 1012
	CloseTryAgainLater           = 1013
	CloseTLSHandshake            = 1015
)

// Duplicate of codes from gorilla/websocket for convenience.
var validReceivedCloseCodes = map[int]bool{
	// see http://www.iana.org/assignments/websocket/websocket.xhtml#close-code-number

	CloseNormalClosure:           true,
	CloseGoingAway:               true,
	CloseProtocolError:           true,
	CloseUnsupportedData:         true,
	CloseNoStatusReceived:        false,
	CloseAbnormalClosure:         false,
	CloseInvalidFramePayloadData: true,
	ClosePolicyViolation:         true,
	CloseMessageTooBig:           true,
	CloseMandatoryExtension:      true,
	CloseInternalServerErr:       true,
	CloseServiceRestart:          true,
	CloseTryAgainLater:           true,
	CloseTLSHandshake:            false,
}

// Keep alive const
const (
	KeepAliveOff = iota
	KeepAlivePing
	KeepAlivePong
)

// KeepAliveMode - Keep alive mode type
type KeepAliveMode int

type handleMessageFunc func(*Session, []byte)
type handleErrorFunc func(*Session, error)
type handleCloseFunc func(*Session, int, string) error
type handleSessionFunc func(*Session)
type filterFunc func(*Session) bool

// Melody implements a websocket manager.
type Melody struct {
	Config                   *Config
	Upgrader                 *websocket.Upgrader
	messageHandler           handleMessageFunc
	messageHandlerBinary     handleMessageFunc
	messageSentHandler       handleMessageFunc
	messageSentHandlerBinary handleMessageFunc
	errorHandler             handleErrorFunc
	closeHandler             handleCloseFunc
	connectHandler           handleSessionFunc
	disconnectHandler        handleSessionFunc
	pingHandler              handleSessionFunc
	pongHandler              handleSessionFunc
	hub                      *hub
	KeepAlive                KeepAliveMode
	pubMutex                 sync.RWMutex
	debugInfo                DebugInfo
}

// DebugInfo -
type DebugInfo struct {
	UserCountMutex sync.Mutex
	UserCount      int
}

// EnableDebug -
var EnableDebug = false

// New creates a new melody instance with default Upgrader and Config.
func New() *Melody {
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}

	hub := newHub()

	for i := 0; i < RedisRcvConn; i++ {
		go hub.run(i)
		go hub.readRedisConn(i)
	}

	return &Melody{
		Config:                   newConfig(),
		Upgrader:                 upgrader,
		messageHandler:           func(*Session, []byte) {},
		messageHandlerBinary:     func(*Session, []byte) {},
		messageSentHandler:       func(*Session, []byte) {},
		messageSentHandlerBinary: func(*Session, []byte) {},
		errorHandler:             func(*Session, error) {},
		closeHandler:             nil,
		connectHandler:           func(*Session) {},
		disconnectHandler:        func(*Session) {},
		pingHandler:              func(*Session) {},
		pongHandler:              func(*Session) {},
		hub:                      hub,
		debugInfo:                DebugInfo{},
	}
}

// HandleConnect fires fn when a session connects.
func (m *Melody) HandleConnect(fn func(*Session)) {
	m.connectHandler = fn
}

// HandleDisconnect fires fn when a session disconnects.
func (m *Melody) HandleDisconnect(fn func(*Session)) {
	m.disconnectHandler = fn
}

// HandlePing fires fn when a ping is received from a session.
func (m *Melody) HandlePing(fn func(*Session)) {
	m.pingHandler = fn
}

// HandlePong fires fn when a pong is received from a session.
func (m *Melody) HandlePong(fn func(*Session)) {
	m.pongHandler = fn
}

// HandleMessage fires fn when a text message comes in.
func (m *Melody) HandleMessage(fn func(*Session, []byte)) {
	m.messageHandler = fn
}

// HandleMessageBinary fires fn when a binary message comes in.
func (m *Melody) HandleMessageBinary(fn func(*Session, []byte)) {
	m.messageHandlerBinary = fn
}

// HandleSentMessage fires fn when a text message is successfully sent.
func (m *Melody) HandleSentMessage(fn func(*Session, []byte)) {
	m.messageSentHandler = fn
}

// HandleSentMessageBinary fires fn when a binary message is successfully sent.
func (m *Melody) HandleSentMessageBinary(fn func(*Session, []byte)) {
	m.messageSentHandlerBinary = fn
}

// HandleError fires fn when a session has an error.
func (m *Melody) HandleError(fn func(*Session, error)) {
	m.errorHandler = fn
}

// HandleClose sets the handler for close messages received from the session.
// The code argument to h is the received close code or CloseNoStatusReceived
// if the close message is empty. The default close handler sends a close frame
// back to the session.
//
// The application must read the connection to process close messages as
// described in the section on Control Frames above.
//
// The connection read methods return a CloseError when a close frame is
// received. Most applications should handle close messages as part of their
// normal error handling. Applications should only set a close handler when the
// application must perform some action before sending a close frame back to
// the session.
func (m *Melody) HandleClose(fn func(*Session, int, string) error) {
	if fn != nil {
		m.closeHandler = fn
	}
}

// HandleRequest upgrades http requests to websocket connections and dispatches them to be handled by the melody instance.
func (m *Melody) HandleRequest(w http.ResponseWriter, r *http.Request) error {
	return m.HandleRequestWithKeys(w, r, nil)
}

// HandleRequestWithKeys does the same as HandleRequest but populates session.Keys with keys.
func (m *Melody) HandleRequestWithKeys(w http.ResponseWriter, r *http.Request, keys map[string]interface{}) error {
	if m.hub.closed() {
		return errors.New("melody instance is closed")
	}

	conn, err := m.Upgrader.Upgrade(w, r, nil)

	if err != nil {
		return err
	}

	if EnableDebug {
		m.debugInfo.UserCountMutex.Lock()
		m.debugInfo.UserCount++
		log.Print("Enter User count:[", m.debugInfo.UserCount, "]", " NumGoroutine:[", runtime.NumGoroutine(), "]")
		m.debugInfo.UserCountMutex.Unlock()
	}

	session := &Session{
		Request:     r,
		Keys:        keys,
		conn:        conn,
		output:      make(chan *envelope, m.Config.MessageBufferSize),
		melody:      m,
		open:        true,
		rwmutex:     &sync.RWMutex{},
		RegMap:      make(map[string]interface{}),
		regMapMutex: &sync.RWMutex{},
	}

	m.hub.register <- session

	m.connectHandler(session)

	go session.writePump()

	session.readPump()

	// Ensure session's registration in channel
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			registered := m.hub.registered(session)
			if registered {
				return
			}
		}
	}()
	wg.Wait()

	if !m.hub.closed() {
		m.hub.unregister <- session
	}

	session.close()

	m.disconnectHandler(session)

	if EnableDebug {
		m.debugInfo.UserCountMutex.Lock()
		m.debugInfo.UserCount--
		log.Print("Leave User count:[", m.debugInfo.UserCount, "]", " NumGoroutine:[", runtime.NumGoroutine(), "]")
		m.debugInfo.UserCountMutex.Unlock()
	}

	return nil
}

// Broadcast broadcasts a text message to all sessions.
func (m *Melody) Broadcast(msg []byte, channel interface{}, messageType int) error {
	return m.BroadcastOthers(nil, msg, channel, messageType)
}

// BroadcastFilter broadcasts a text message to all sessions that fn returns true for.
func (m *Melody) BroadcastFilter(msg []byte, fn func(*Session) bool) error {
	if m.hub.closed() {
		return errors.New("melody instance is closed")
	}

	message := &envelope{T: websocket.TextMessage, Msg: msg, filter: fn}
	m.hub.broadcast[0] <- message

	return nil
}

// BroadcastRemote -
func (m *Melody) BroadcastRemote(msg []byte, channel interface{}, messageType int) error {
	return m.BroadcastOthersRemote(nil, msg, channel, messageType)
}

// BroadcastOthersRemote -
func (m *Melody) BroadcastOthersRemote(s *Session, msg []byte, channel interface{}, messageType int) error {
	if m.hub.closed() {
		return errors.New("melody instance is closed")
	}
	message := &envelope{T: messageType, Msg: msg, To: channel, From: uintptr(unsafe.Pointer(s))}

	if message.To != "" {
		content, _ := json.Marshal(*message)

		if UseRedisPool {
			m.pubMutex.Lock()
			c := m.hub.redisPool.Get()
			if c != nil {
				c.Do("PUBLISH", message.To, content)
			}
			defer c.Close()
			m.pubMutex.Unlock()
		} else {
			m.pubMutex.Lock()
			if m.hub.pubRedisConn == nil {
				redisURI := RedisURL
				if c, err := gRedisConn(redisURI); err != nil {
					log.Printf("error on redis conn. %s\n", err)
				} else {
					m.hub.pubRedisConn = c
				}
			}

			if m.hub.pubRedisConn != nil {
				m.hub.pubRedisConn.Do("PUBLISH", message.To, content)
			}
			m.pubMutex.Unlock()
		}
	}

	return nil
}

// BroadcastOthers broadcasts a text message to all sessions except session s.
func (m *Melody) BroadcastOthers(s *Session, msg []byte, channel interface{}, messageType int) error {
	if m.hub.closed() {
		return errors.New("melody instance is closed")
	}

	message := &envelope{T: messageType, Msg: msg, To: channel, From: uintptr(unsafe.Pointer(s))}
	m.hub.regMutex.RLock()
	bucketIdxs := make([]int, 0)
	for i, maps := range m.hub.routeMaps {
		_, ok := maps[channel]
		if ok {
			bucketIdxs = append(bucketIdxs, i)
		}
	}
	m.hub.regMutex.RUnlock()
	for _, bucketIdx := range bucketIdxs {
		m.hub.broadcast[bucketIdx] <- message
	}

	return nil
}

// BroadcastMultiple broadcasts a text message to multiple sessions given in the sessions slice.
func (m *Melody) BroadcastMultiple(msg []byte, sessions []*Session) error {
	for _, sess := range sessions {
		if writeErr := sess.Write(msg); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

// BroadcastBinary broadcasts a binary message to all sessions.
func (m *Melody) BroadcastBinary(msg []byte) error {
	if m.hub.closed() {
		return errors.New("melody instance is closed")
	}

	message := &envelope{T: websocket.BinaryMessage, Msg: msg}
	m.hub.broadcast[0] <- message

	return nil
}

// BroadcastBinaryFilter broadcasts a binary message to all sessions that fn returns true for.
func (m *Melody) BroadcastBinaryFilter(msg []byte, fn func(*Session) bool) error {
	if m.hub.closed() {
		return errors.New("melody instance is closed")
	}

	message := &envelope{T: websocket.BinaryMessage, Msg: msg, filter: fn}
	m.hub.broadcast[0] <- message

	return nil
}

// BroadcastBinaryOthers broadcasts a binary message to all sessions except session s.
func (m *Melody) BroadcastBinaryOthers(msg []byte, s *Session) error {
	return m.BroadcastBinaryFilter(msg, func(q *Session) bool {
		return s != q
	})
}

// Close closes the melody instance and all connected sessions.
func (m *Melody) Close() error {
	if m.hub.closed() {
		return errors.New("melody instance is already closed")
	}

	m.hub.exit <- &envelope{T: websocket.CloseMessage, Msg: []byte{}}

	return nil
}

// CloseWithMsg closes the melody instance with the given close payload and all connected sessions.
// Use the FormatCloseMessage function to format a proper close message payload.
func (m *Melody) CloseWithMsg(msg []byte) error {
	if m.hub.closed() {
		return errors.New("melody instance is already closed")
	}

	m.hub.exit <- &envelope{T: websocket.CloseMessage, Msg: msg}

	return nil
}

// Len return the number of connected sessions.
func (m *Melody) Len() int {
	return m.hub.len()
}

// IsClosed returns the status of the melody instance.
func (m *Melody) IsClosed() bool {
	return m.hub.closed()
}

// FormatCloseMessage formats closeCode and text as a WebSocket close message.
func FormatCloseMessage(closeCode int, text string) []byte {
	return websocket.FormatCloseMessage(closeCode, text)
}
