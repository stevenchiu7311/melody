package melody

import (
	"encoding/json"
	"log"
	"sync"
	"time"
	"unsafe"

	"github.com/gomodule/redigo/redis"
)

var (
	gRedisConn = func(uri string) (redis.Conn, error) {
		redisConn, err := redis.Dial("tcp", uri,
			redis.DialConnectTimeout(time.Duration(10*time.Second)),
			redis.DialReadTimeout(time.Duration(0)),
			redis.DialWriteTimeout(time.Duration(0)))
		return redisConn, err
	}
	forkInHub       = true
	routeMapping    = true
	bucketDebugDump = false
)

type hub struct {
	sessions       map[*Session]bool
	broadcast      []chan *envelope
	register       chan *Session
	exit           chan *envelope
	unregister     chan *Session
	persistRecv    []chan bool
	open           bool
	rwmutex        *sync.RWMutex
	redisPool      *redis.Pool
	redisConn      redis.Conn
	pubRedisConn   redis.Conn
	pubSubConn     []*redis.PubSubConn
	regMutex       *sync.RWMutex
	allocConnMutex *sync.Mutex
	routeMaps      []map[interface{}]map[*Session]*Session
}

func newRedisPool() *redis.Pool {
	statRedis := newStatsRedis(RedisURL)
	numRedisConn := statRedis.MaxActive
	if DebugRedisPoolConn != 0 {
		numRedisConn = DebugRedisPoolConn
	}
	log.Println("Configured max active redis conn:", numRedisConn)
	return &redis.Pool{
		MaxIdle:   statRedis.MaxIdle,
		MaxActive: numRedisConn,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", statRedis.URL,
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
	redisURI := RedisURL
	log.Printf("Connect to redis server:[%s]\n", redisURI)
	log.Println("Configured max recv redis conn:", RedisRcvConn)
	routeMaps := make([]map[interface{}]map[*Session]*Session, RedisRcvConn)
	pubSubConn := make([]*redis.PubSubConn, RedisRcvConn)
	broadcast := make([]chan *envelope, RedisRcvConn)
	persistRecv := make([]chan bool, RedisRcvConn)
	regRefMaps := make([]map[string]*int, RedisRcvConn)
	for i := 0; i < RedisRcvConn; i++ {
		redisConn, err := gRedisConn(redisURI)
		pubSubConn[i] = &redis.PubSubConn{Conn: redisConn}
		if err != nil {
			panic(err)
		}
		broadcast[i] = make(chan *envelope)
		regRefMaps[i] = make(map[string]*int)
		routeMaps[i] = make(map[interface{}]map[*Session]*Session)
		persistRecv[i] = make(chan bool)
	}

	//defer redisConn.Close()

	//defer pubSubConn.Close()

	return &hub{
		sessions:    make(map[*Session]bool),
		broadcast:   broadcast,
		register:    make(chan *Session),
		unregister:  make(chan *Session),
		exit:        make(chan *envelope),
		persistRecv: persistRecv,
		open:        true,
		rwmutex:     &sync.RWMutex{},
		redisPool:   redisPool,
		pubSubConn:  pubSubConn,
		regMutex:    &sync.RWMutex{},
		routeMaps:   routeMaps,
	}
}

func (h *hub) run(index int) {
loop:
	for {
		select {
		case s := <-h.register:
			h.rwmutex.Lock()
			h.sessions[s] = true
			h.rwmutex.Unlock()
		case s := <-h.unregister:
			h.rwmutex.Lock()
			if _, ok := h.sessions[s]; ok {
				delete(h.sessions, s)
			}
			h.rwmutex.Unlock()
		case m := <-h.broadcast[index]:
			handler := func() {
				h.rwmutex.RLock()
				if false {
					log.Println(m)
				}
				if routeMapping {
					h.regMutex.Lock()
					for s := range h.routeMaps[index][m.To] {
						if m.filter != nil {
							if m.filter(s) {
								s.writeMessage(m)
							}
						} else {
							s.writeMessage(m)
						}
					}
					h.regMutex.Unlock()
				} else {
					for s := range h.sessions {
						if m.filter != nil {
							if m.filter(s) {
								s.writeMessage(m)
							}
						} else {
							s.writeMessage(m)
						}
					}
				}
				h.rwmutex.RUnlock()
			}
			if forkInHub {
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
			for i := 0; i < RedisRcvConn; i++ {
				h.persistRecv[i] <- false
			}
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

func (h *hub) registered(session *Session) bool {
	h.rwmutex.RLock()
	defer h.rwmutex.RUnlock()

	return h.sessions[session]
}

func (h *hub) readRedisConn(index int) {
	if bucketDebugDump {
		go func() {
			for {
				h.regMutex.Lock()
				if len(h.routeMaps[index]) > 0 {
					log.Println("-------------------------------------")
					log.Printf("h.routeMaps[%d]:%d", index, len(h.routeMaps[index]))
				}
				h.regMutex.Unlock()
				time.Sleep(5 * time.Second)
			}
		}()
	}

	for {
		switch v := h.pubSubConn[index].Receive().(type) {
		case redis.Message:
			handler := func() {
				e := &envelope{}
				json.Unmarshal(v.Data, e)
				message := &envelope{T: e.T, Msg: []byte(e.Msg), filter: func(s *Session) bool {
					if e.From != 0 && e.From == uintptr(unsafe.Pointer(s)) {
						return false
					}
					if routeMapping {
						return true
					}

					s.regMapMutex.RLock()
					for _, element := range s.RegMap {
						if element == e.To {
							s.regMapMutex.RUnlock()
							return true
						}
					}
					s.regMapMutex.RUnlock()
					return false
				}, To: e.To}

				h.broadcast[index] <- message
			}
			if forkInHub {
				go handler()
			} else {
				handler()
			}
		case redis.Subscription:
			if EnableDebug {
				log.Printf("subscription message:[%s] count:[%d] recvThread[%d]\n", v.Channel, v.Count, index)
			}
		case error:
			log.Println("error pub/sub on connection, delivery has stopped, err[", v, "]")
			var persistRecv = true
			select {
			case persistRecv = <-h.persistRecv[index]:
			case <-time.After(time.Duration(RedisRcvRetryInterval) * time.Second):
			}
			if persistRecv {
				redisURI := RedisURL
				redisConn, err := gRedisConn(redisURI)
				h.pubSubConn[index] = &redis.PubSubConn{Conn: redisConn}
				if err == nil {
					log.Println("Re-connect redis")
				} else {
					continue
				}

				h.regMutex.Lock()
				for key := range h.routeMaps[index] {
					if err := h.pubSubConn[index].Subscribe(key); err != nil {
						h.regMutex.Unlock()
						panic(err)
					}
				}
				h.regMutex.Unlock()
			} else {
				return
			}
		}
	}
}
