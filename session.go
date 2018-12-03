package melody

import (
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"cmcm.com/cmgs/app/core"
	"github.com/gomodule/redigo/redis"
	"github.com/gorilla/websocket"
)

// Session wrapper around websocket connections.
type Session struct {
	Request *http.Request
	Keys    map[string]interface{}
	conn    *websocket.Conn
	output  chan *envelope
	melody  *Melody
	open    bool
	rwmutex *sync.RWMutex
	RegMap  map[string]interface{}
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

	if s.melody.KeepAlive {
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
			if s.melody.KeepAlive {
				s.ping()
			}
		}
	}
}

func (s *Session) readPump() {
	if s.melody.KeepAlive {
		s.conn.SetReadLimit(s.melody.Config.MaxMessageSize)
		s.conn.SetReadDeadline(time.Now().Add(s.melody.Config.PongWait))

		s.conn.SetPongHandler(func(string) error {
			s.conn.SetReadDeadline(time.Now().Add(s.melody.Config.PongWait))
			s.melody.pongHandler(s)
			return nil
		})
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
	defer func() {
		if r := recover(); r != nil {
			redisURI := core.ConfString("REDIS_URI")
			redisConn, err := gRedisConn(redisURI)
			s.melody.hub.pubSubConn = &redis.PubSubConn{Conn: redisConn}
			if err == nil {
				log.Println("Re-connect redis")
			} else {
				panic(err)
			}
			s.register(regMap)
			s.melody.hub.reconnect <- true
		}
	}()

	s.register(regMap)
}

func (s *Session) register(regMap map[string]interface{}) {
	for key, element := range regMap {
		s.RegMap[key] = element
		v, ok := s.melody.hub.regRefMap[element.(string)]
		if ok {
			*v++
		} else {
			if err := s.melody.hub.pubSubConn.Subscribe(element); err != nil {
				panic(err)
			} else {
				i := 1
				s.melody.hub.regRefMap[element.(string)] = &i
			}
		}
	}
}

func (s *Session) Unregister(regMap map[string]interface{}) {
	for key, element := range regMap {
		delete(s.RegMap, key)
		v, ok := s.melody.hub.regRefMap[element.(string)]
		if ok {
			*v--
			if *v == 0 {
				delete(s.melody.hub.regRefMap, element.(string))
				if err := s.melody.hub.pubSubConn.Unsubscribe(element); err != nil {
					panic(err)
				}
			}
		}
	}
}
