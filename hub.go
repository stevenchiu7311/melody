package melody

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"cmcm.com/cmgs/app/core"
	"github.com/gomodule/redigo/redis"
	"github.com/gorilla/websocket"
)

var (
	gRedisConn = func(uri string) (redis.Conn, error) {
		redisConn, err := redis.Dial("tcp", uri,
			redis.DialConnectTimeout(time.Duration(10*time.Second)),
			redis.DialReadTimeout(time.Duration(0)),
			redis.DialWriteTimeout(time.Duration(0)))
		return redisConn, err
	}
	ForkInHub = true
)

type hub struct {
	sessions     map[*Session]bool
	broadcast    chan *envelope
	register     chan *Session
	exit         chan *envelope
	unregister   chan *Session
	persistRecv  chan bool
	open         bool
	rwmutex      *sync.RWMutex
	redisPool    *redis.Pool
	redisConn    redis.Conn
	pubRedisConn redis.Conn
	pubSubConn   *redis.PubSubConn
	regRefMap    map[string]*int
	regMutex     *sync.Mutex
}

func newRedisPool() *redis.Pool {
	statRedis := newStatsRedis()
	numRedisConn := statRedis.MaxActive
	if DebugRedisPoolConn != 0 {
		numRedisConn = DebugRedisPoolConn
	}
	log.Println("Configured max active redis conn:", numRedisConn)
	return &redis.Pool{
		MaxIdle:   statRedis.MaxIdle,
		MaxActive: numRedisConn,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", core.ConfString("REDIS_URI"),
				redis.DialConnectTimeout(time.Duration(statRedis.ConnectTimeout)),
				redis.DialReadTimeout(time.Duration(statRedis.ReadTimeout)),
				redis.DialWriteTimeout(time.Duration(statRedis.WriteTimeout)))
			if err != nil {
				log.Println(err)
				return nil, err
			}
			return c, nil
		},
		// Use the TestOnBorrow function to check the health of an idle connection
		// before the connection is returned to the application.
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			if time.Since(t) < time.Minute {
				return nil
			}
			_, err := c.Do("PING")
			return err
		},
		IdleTimeout: 300 * time.Second,
		// If Wait is true and the pool is at the MaxActive limit,
		// then Get() waits for a connection to be returned to the pool before returning
		Wait: true,
	}
}
func newHub() *hub {
	redisPool := newRedisPool()
	redisURI := core.ConfString("REDIS_URI")
	log.Printf("Connect to redis server:[%s]\n", redisURI)
	redisConn, err := gRedisConn(redisURI)
	if err != nil {
		panic(err)
	}
	//defer redisConn.Close()

	//defer pubSubConn.Close()

	return &hub{
		sessions:    make(map[*Session]bool),
		broadcast:   make(chan *envelope),
		register:    make(chan *Session),
		unregister:  make(chan *Session),
		exit:        make(chan *envelope),
		persistRecv: make(chan bool, 1),
		open:        true,
		rwmutex:     &sync.RWMutex{},
		redisPool:   redisPool,
		pubSubConn:  &redis.PubSubConn{Conn: redisConn},
		regRefMap:   make(map[string]*int),
		regMutex:    &sync.Mutex{},
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
			handler := func() {
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
			}
			if ForkInHub {
				go handler()
			} else {
				handler()
			}
		case m := <-h.exit:
			h.rwmutex.Lock()
			for s := range h.sessions {
				s.writeMessage(m)
				delete(h.sessions, s)
				s.Close()
			}
			h.open = false
			h.rwmutex.Unlock()
			h.persistRecv <- false
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
			handler := func() {
				e := &envelope{}
				json.Unmarshal(v.Data, e)
				message := &envelope{T: websocket.TextMessage, Msg: []byte(e.Msg), filter: func(s *Session) bool {
					s.regMapMutex.RLock()
					for _, element := range s.RegMap {
						if element == e.To {
							s.regMapMutex.RUnlock()
							return true
						}
					}
					s.regMapMutex.RUnlock()
					return false
				}}
				h.broadcast <- message
			}
			if ForkInHub {
				go handler()
			} else {
				handler()
			}
		case redis.Subscription:
			log.Printf("subscription message:[%s] count:[%d]\n", v.Channel, v.Count)
		case error:
			log.Println("error pub/sub on connection, delivery has stopped, err[", v, "]")
			persistRecv := <-h.persistRecv
			if persistRecv {
				for key := range h.regRefMap {
					if err := h.pubSubConn.Subscribe(key); err != nil {
						panic(err)
					}
				}
			} else {
				return
			}
		}
	}
}
