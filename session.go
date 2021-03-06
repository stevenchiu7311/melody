package melody

import (
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
)

// Session wrapper around websocket connections.
type Session struct {
	Request     *http.Request
	Keys        map[string]interface{}
	conn        *websocket.Conn
	output      chan *envelope
	melody      *Melody
	open        bool
	rwmutex     *sync.RWMutex
	RegMap      map[string]interface{}
	regMapMutex *sync.RWMutex
}

func (s *Session) writeMessage(message *envelope) {
	if s.closed() {
		s.melody.errorHandler(s, errors.New("tried to write to closed a session"))
		return
	}

	select {
	case s.output <- message:
	default:
		s.melody.errorHandler(s, errors.New("session message buffer is full"))
	}
}

func (s *Session) writeRaw(message *envelope) error {
	if s.closed() {
		return errors.New("tried to write to a closed session")
	}

	if s.melody.KeepAlive != KeepAliveOff {
		s.conn.SetWriteDeadline(time.Now().Add(s.melody.Config.WriteWait))
	}
	err := s.conn.WriteMessage(message.T, message.Msg)

	if err != nil {
		return err
	}

	return nil
}

func (s *Session) closed() bool {
	s.rwmutex.RLock()
	defer s.rwmutex.RUnlock()

	return !s.open
}

func (s *Session) close() {
	if !s.closed() {
		s.rwmutex.Lock()
		s.open = false
		s.conn.Close()
		close(s.output)
		s.rwmutex.Unlock()
	}
}

func (s *Session) ping() {
	s.writeRaw(&envelope{T: websocket.PingMessage, Msg: []byte{}})
}

func (s *Session) pong() {
	s.writeRaw(&envelope{T: websocket.PongMessage, Msg: []byte{}})
}

func (s *Session) writePump() {
	ticker := time.NewTicker(s.melody.Config.PingPeriod)
	defer ticker.Stop()

loop:
	for {
		select {
		case msg, ok := <-s.output:
			if !ok {
				break loop
			}

			err := s.writeRaw(msg)

			if err != nil {
				s.melody.errorHandler(s, err)
				break loop
			}

			if msg.T == websocket.CloseMessage {
				break loop
			}

			if msg.T == websocket.TextMessage {
				s.melody.messageSentHandler(s, msg.Msg)
			}

			if msg.T == websocket.BinaryMessage {
				s.melody.messageSentHandlerBinary(s, msg.Msg)
			}
		case <-ticker.C:
			if s.melody.KeepAlive == KeepAlivePing {
				s.ping()
			}
		}
	}
}

func (s *Session) readPump() {
	s.conn.SetReadLimit(s.melody.Config.MaxMessageSize)
	if s.melody.KeepAlive != KeepAliveOff {
		s.conn.SetReadDeadline(time.Now().Add(s.melody.Config.PongWait))
		if s.melody.KeepAlive == KeepAlivePong {
			s.conn.SetPingHandler(func(string) error {
				s.conn.SetReadDeadline(time.Now().Add(s.melody.Config.PongWait))
				s.melody.pingHandler(s)
				s.pong()
				return nil
			})
		}
		if s.melody.KeepAlive == KeepAlivePing {
			s.conn.SetPongHandler(func(string) error {
				s.conn.SetReadDeadline(time.Now().Add(s.melody.Config.PongWait))
				s.melody.pongHandler(s)
				return nil
			})
		}
	}

	if s.melody.closeHandler != nil {
		s.conn.SetCloseHandler(func(code int, text string) error {
			return s.melody.closeHandler(s, code, text)
		})
	}

	for {
		t, message, err := s.conn.ReadMessage()

		if err != nil {
			s.melody.errorHandler(s, err)
			break
		}

		if t == websocket.TextMessage {
			s.melody.messageHandler(s, message)
		}

		if t == websocket.BinaryMessage {
			s.melody.messageHandlerBinary(s, message)
		}

		if s.melody.KeepAlive != KeepAliveOff {
			s.conn.SetReadDeadline(time.Now().Add(s.melody.Config.PongWait))
		}
	}
}

// Write writes message to session.
func (s *Session) Write(msg []byte) error {
	if s.closed() {
		return errors.New("session is closed")
	}

	s.writeMessage(&envelope{T: websocket.TextMessage, Msg: msg})

	return nil
}

// WriteBinary writes a binary message to session.
func (s *Session) WriteBinary(msg []byte) error {
	if s.closed() {
		return errors.New("session is closed")
	}

	s.writeMessage(&envelope{T: websocket.BinaryMessage, Msg: msg})

	return nil
}

// Close closes session.
func (s *Session) Close() error {
	if s.closed() {
		return errors.New("session is already closed")
	}

	s.writeMessage(&envelope{T: websocket.CloseMessage, Msg: []byte{}})

	return nil
}

// CloseWithMsg closes the session with the provided payload.
// Use the FormatCloseMessage function to format a proper close message payload.
func (s *Session) CloseWithMsg(msg []byte) error {
	if s.closed() {
		return errors.New("session is already closed")
	}

	s.writeMessage(&envelope{T: websocket.CloseMessage, Msg: msg})

	return nil
}

// Set is used to store a new key/value pair exclusivelly for this session.
// It also lazy initializes s.Keys if it was not used previously.
func (s *Session) Set(key string, value interface{}) {
	if s.Keys == nil {
		s.Keys = make(map[string]interface{})
	}

	s.Keys[key] = value
}

// Get returns the value for the given key, ie: (value, true).
// If the value does not exists it returns (nil, false)
func (s *Session) Get(key string) (value interface{}, exists bool) {
	if s.Keys != nil {
		value, exists = s.Keys[key]
	}

	return
}

// MustGet returns the value for the given key if it exists, otherwise it panics.
func (s *Session) MustGet(key string) interface{} {
	if value, exists := s.Get(key); exists {
		return value
	}

	panic("Key \"" + key + "\" does not exist")
}

// IsClosed returns the status of the connection.
func (s *Session) IsClosed() bool {
	return s.closed()
}

func (s *Session) Register(regMap map[string]interface{}) {
	retryConn := false
	value := int64(uintptr(unsafe.Pointer(s)))
	bucketIdx := indexFor(value, RedisRcvConn)
	for key, element := range regMap {
		s.regMapMutex.Lock()
		s.RegMap[key] = element
		s.regMapMutex.Unlock()

		s.melody.hub.regMutex.Lock()
		_, ok := s.melody.hub.routeMaps[bucketIdx][element.(string)]
		if !ok {
			s.melody.hub.routeMaps[bucketIdx][element.(string)] = make(map[*Session]*Session)
			if err := s.melody.hub.pubSubConn[bucketIdx].Subscribe(element); err != nil {
				log.Println("Subscribe [", element, "] failed...\n", err)
				retryConn = true
			}
		}
		s.melody.hub.routeMaps[bucketIdx][element.(string)][s] = s
		s.melody.hub.regMutex.Unlock()
	}

	if retryConn {
		s.signalRetry(bucketIdx)
	}
}

func (s *Session) Unregister(regMap map[string]interface{}) {
	value := int64(uintptr(unsafe.Pointer(s)))
	bucketIdx := indexFor(value, RedisRcvConn)
	for key, element := range regMap {
		s.regMapMutex.Lock()
		delete(s.RegMap, key)
		s.regMapMutex.Unlock()

		s.melody.hub.regMutex.Lock()
		_, ok := s.melody.hub.routeMaps[bucketIdx][element.(string)]
		if ok {
			delete(s.melody.hub.routeMaps[bucketIdx][element.(string)], s)
			if len(s.melody.hub.routeMaps[bucketIdx][element.(string)]) == 0 {
				delete(s.melody.hub.routeMaps[bucketIdx], element.(string))
				if err := s.melody.hub.pubSubConn[bucketIdx].Unsubscribe(element); err != nil {
					log.Println("Unsubscribe [", element, "] failed...\n", err)
				}
			}
		}
		s.melody.hub.regMutex.Unlock()
	}
}

func (s *Session) signalRetry(index int) {
	select {
	case s.melody.hub.persistRecv[index] <- true:
		log.Println("Send persistRecv signal...")
	default:
		log.Println("Already being reconnected and drop persistRecv signal...")
	}
}
