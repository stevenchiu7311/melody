package melody

import (
	"encoding/json"
	"log"
	"sync"

	"cmcm.com/cmgs/app/core"
	"github.com/gomodule/redigo/redis"
	"github.com/gorilla/websocket"
)

var (
	gRedisConn = func() (redis.Conn, error) {
		return redis.Dial("tcp", ":6379")
	}
)

type hub struct {
	sessions   map[*Session]bool
	broadcast  chan *envelope
	register   chan *Session
	unregister chan *Session
	exit       chan *envelope
	open       bool
	rwmutex    *sync.RWMutex
	redisConn  redis.Conn
	pubSubConn *redis.PubSubConn
	regRefMap  map[string]*int
}

func newHub() *hub {
	redisURI := core.ConfString("REDIS_URI")
	log.Printf("Connect to redis server:[%s]\n", redisURI)
	redisConn, err := redis.Dial("tcp", redisURI)
	if err != nil {
		panic(err)
	}
	//defer redisConn.Close()

	//defer pubSubConn.Close()

	return &hub{
		sessions:   make(map[*Session]bool),
		broadcast:  make(chan *envelope),
		register:   make(chan *Session),
		unregister: make(chan *Session),
		exit:       make(chan *envelope),
		open:       true,
		rwmutex:    &sync.RWMutex{},
		redisConn:  redisConn,
		pubSubConn: &redis.PubSubConn{Conn: redisConn},
		regRefMap:  make(map[string]*int),
	}
}

func (h *hub) run() {
loop:
	for {
		select {
		case s := <-h.register:
			h.rwmutex.Lock()
			h.sessions[s] = true
			h.rwmutex.Unlock()
		case s := <-h.unregister:
			if _, ok := h.sessions[s]; ok {
				h.rwmutex.Lock()
				delete(h.sessions, s)
				h.rwmutex.Unlock()
			}
		case m := <-h.broadcast:
			h.rwmutex.RLock()
			for s := range h.sessions {
				if m.filter != nil {
					if m.filter(s) {
						s.writeMessage(m)
					}
				} else {
					s.writeMessage(m)
				}
			}
			h.rwmutex.RUnlock()
		case m := <-h.exit:
			h.rwmutex.Lock()
			for s := range h.sessions {
				s.writeMessage(m)
				delete(h.sessions, s)
				s.Close()
			}
			h.open = false
			h.rwmutex.Unlock()
			break loop
		}
	}
}

func (h *hub) closed() bool {
	h.rwmutex.RLock()
	defer h.rwmutex.RUnlock()
	return !h.open
}

func (h *hub) len() int {
	h.rwmutex.RLock()
	defer h.rwmutex.RUnlock()

	return len(h.sessions)
}

func (h *hub) readRedisConn() {
	for {
		switch v := h.pubSubConn.Receive().(type) {
		case redis.Message:
			e := &envelope{}
			json.Unmarshal(v.Data, e)
			message := &envelope{T: websocket.TextMessage, Msg: []byte(e.Msg), filter: func(s *Session) bool {
				for _, element := range s.RegMap {
					if element == e.To {
						return true
					}
				}
				return false
			}}
			h.broadcast <- message
		case redis.Subscription:
			log.Printf("subscription message:[%s] count:[%d]\n", v.Channel, v.Count)
		case error:
			log.Println("error pub/sub on connection, delivery has stopped")
			return
		}
	}
}
