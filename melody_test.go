package melody

import (
	"bytes"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/quick"
	"time"

	"github.com/gorilla/websocket"
)

var (
	testChannel = "test"
)

type TestServer struct {
	m *Melody
}

func NewTestServerHandler(handler handleMessageFunc) *TestServer {
	m := New()
	m.HandleMessage(handler)
	return &TestServer{
		m: m,
	}
}

func NewTestServer() *TestServer {
	m := New()
	return &TestServer{
		m: m,
	}
}

func (s *TestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.m.HandleRequest(w, r)
}

func NewDialer(url string) (*websocket.Conn, error) {
	dialer := &websocket.Dialer{}
	conn, _, err := dialer.Dial(strings.Replace(url, "http", "ws", 1), nil)
	return conn, err
}

func TestEcho(t *testing.T) {
	log.Println("TestEcho")
	echo := NewTestServerHandler(func(session *Session, msg []byte) {
		session.Write(msg)
	})
	server := httptest.NewServer(echo)
	defer server.Close()

	fn := func(msg string) bool {
		conn, err := NewDialer(server.URL)
		defer conn.Close()

		if err != nil {
			t.Error(err)
			return false
		}

		conn.WriteMessage(websocket.TextMessage, []byte(msg))

		_, ret, err := conn.ReadMessage()

		if err != nil {
			t.Error(err)
			return false
		}

		if msg != string(ret) {
			t.Errorf("%s should equal %s", msg, string(ret))
			return false
		}

		return true
	}

	if err := quick.Check(fn, nil); err != nil {
		t.Error(err)
	}
}

func TestWriteClosed(t *testing.T) {
	log.Println("TestWriteClosed")
	echo := NewTestServerHandler(func(session *Session, msg []byte) {
		session.Write(msg)
	})
	echo.m.HandleConnect(func(s *Session) {
		s.Close()
	})

	echo.m.HandleDisconnect(func(s *Session) {
		err := s.Write([]byte("hello world"))

		if err == nil {
			t.Error("should be an error")
		}
	})
	server := httptest.NewServer(echo)
	defer server.Close()

	fn := func(msg string) bool {
		conn, err := NewDialer(server.URL)

		if err != nil {
			t.Error(err)
			return false
		}

		conn.WriteMessage(websocket.TextMessage, []byte(msg))

		return true
	}

	if err := quick.Check(fn, nil); err != nil {
		t.Error(err)
	}
}

func TestLen(t *testing.T) {
	for count := 0; count < 10; count++ {
		t.Run("TestLen", func(t *testing.T) {
			t.Parallel()
			rand.Seed(time.Now().UnixNano())

			connect := int(rand.Int31n(100))
			disconnect := rand.Float32()
			conns := make([]*websocket.Conn, connect)

			m := NewWithTag(t.Name())
			echo := &TestServer{
				m: m,
			}

			server := httptest.NewServer(echo)
			defer server.Close()

			disconnected := 0
			for i := 0; i < connect; i++ {
				conn, err := NewDialer(server.URL)

				if err != nil {
					t.Error(err)
				}

				if rand.Float32() < disconnect {
					conns[i] = nil
					disconnected++
					conn.Close()
					continue
				}

				conns[i] = conn
			}
			block := make(chan int)
			go func() {
				time.Sleep(1 * time.Second)

				connected := connect - disconnected

				if echo.m.Len() != connected {
					t.Errorf("melody len %d should equal %d", echo.m.Len(), connected)
				}
				block <- 0
			}()
			<-block

			func() {
				for _, conn := range conns {
					if conn != nil {
						conn.Close()
					}
				}
			}()
		})
	}
}

func TestEchoBinary(t *testing.T) {
	log.Println("TestEchoBinary")
	echo := NewTestServer()
	echo.m.HandleMessageBinary(func(session *Session, msg []byte) {
		session.WriteBinary(msg)
	})
	server := httptest.NewServer(echo)
	defer server.Close()

	fn := func(msg string) bool {
		conn, err := NewDialer(server.URL)
		defer conn.Close()

		if err != nil {
			t.Error(err)
			return false
		}

		conn.WriteMessage(websocket.BinaryMessage, []byte(msg))

		_, ret, err := conn.ReadMessage()

		if err != nil {
			t.Error(err)
			return false
		}

		if msg != string(ret) {
			t.Errorf("%s should equal %s", msg, string(ret))
			return false
		}

		return true
	}

	if err := quick.Check(fn, nil); err != nil {
		t.Error(err)
	}
}

func TestHandlers(t *testing.T) {
	log.Println("TestHandlers")
	echo := NewTestServer()
	echo.m.HandleMessage(func(session *Session, msg []byte) {
		session.Write(msg)
	})
	server := httptest.NewServer(echo)
	defer server.Close()

	var q *Session

	echo.m.HandleConnect(func(session *Session) {
		q = session
		session.Close()
	})

	echo.m.HandleDisconnect(func(session *Session) {
		if q != session {
			t.Error("disconnecting session should be the same as connecting")
		}
	})

	NewDialer(server.URL)
}

func TestMetadata(t *testing.T) {
	log.Println("TestMetadata")
	echo := NewTestServer()
	echo.m.HandleConnect(func(session *Session) {
		session.Set("stamp", time.Now().UnixNano())
	})
	echo.m.HandleMessage(func(session *Session, msg []byte) {
		stamp := session.MustGet("stamp").(int64)
		session.Write([]byte(strconv.Itoa(int(stamp))))
	})
	server := httptest.NewServer(echo)
	defer server.Close()

	fn := func(msg string) bool {
		conn, err := NewDialer(server.URL)
		defer conn.Close()

		if err != nil {
			t.Error(err)
			return false
		}

		conn.WriteMessage(websocket.TextMessage, []byte(msg))

		_, ret, err := conn.ReadMessage()

		if err != nil {
			t.Error(err)
			return false
		}

		stamp, err := strconv.Atoi(string(ret))

		if err != nil {
			t.Error(err)
			return false
		}

		diff := int(time.Now().UnixNano()) - stamp

		if diff < 0 {
			t.Errorf("diff should be above 0 %d", diff)
			return false
		}

		return true
	}

	if err := quick.Check(fn, nil); err != nil {
		t.Error(err)
	}
}

func TestUpgrader(t *testing.T) {
	log.Println("TestUpgrader")
	broadcast := NewTestServer()
	broadcast.m.HandleMessage(func(session *Session, msg []byte) {
		session.Write(msg)
	})
	server := httptest.NewServer(broadcast)
	defer server.Close()

	broadcast.m.Upgrader = &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return false },
	}

	broadcast.m.HandleError(func(session *Session, err error) {
		if err == nil || err.Error() != "websocket: origin not allowed" {
			t.Error("there should be a origin error")
		}
	})

	_, err := NewDialer(server.URL)

	if err == nil || err.Error() != "websocket: bad handshake" {
		t.Error("there should be a badhandshake error")
	}
}

func TestBroadcast(t *testing.T) {
	log.Println("TestBroadcast")
	broadcast := NewTestServer()
	broadcast.m.HandleConnect(func(session *Session) {
		session.Register(map[string]interface{}{testChannel: testChannel})
	})
	broadcast.m.HandleMessage(func(session *Session, msg []byte) {
		broadcast.m.Broadcast(msg, testChannel, websocket.TextMessage)
	})
	server := httptest.NewServer(broadcast)
	defer server.Close()

	n := 10

	fn := func(msg string) bool {
		conn, _ := NewDialer(server.URL)
		defer conn.Close()

		listeners := make([]*websocket.Conn, n)
		for i := 0; i < n; i++ {
			listener, _ := NewDialer(server.URL)
			listeners[i] = listener
			defer listeners[i].Close()
		}

		conn.WriteMessage(websocket.TextMessage, []byte(msg))
		for i := 0; i < n; i++ {
			_, ret, err := listeners[i].ReadMessage()
			if err != nil {
				t.Error(err)
				return false
			}

			if msg != string(ret) {
				t.Errorf("%s should equal %s", msg, string(ret))
				return false
			}
		}
		return true
	}

	if !fn("test") {
		t.Errorf("should not be false")
	}
}

func TestBroadcastBinary(t *testing.T) {
	log.Println("TestBroadcastBinary")
	broadcast := NewTestServer()
	broadcast.m.HandleConnect(func(session *Session) {
		session.Register(map[string]interface{}{testChannel: testChannel})
	})
	broadcast.m.HandleMessageBinary(func(session *Session, msg []byte) {
		broadcast.m.Broadcast(msg, testChannel, websocket.BinaryMessage)
	})
	server := httptest.NewServer(broadcast)
	defer server.Close()

	n := 10

	fn := func(msg []byte) bool {
		conn, _ := NewDialer(server.URL)
		defer conn.Close()

		listeners := make([]*websocket.Conn, n)
		for i := 0; i < n; i++ {
			listener, _ := NewDialer(server.URL)
			listeners[i] = listener
			defer listeners[i].Close()
		}

		conn.WriteMessage(websocket.BinaryMessage, []byte(msg))

		for i := 0; i < n; i++ {
			messageType, ret, err := listeners[i].ReadMessage()

			if err != nil {
				t.Error(err)
				return false
			}

			if messageType != websocket.BinaryMessage {
				t.Errorf("message type should be BinaryMessage")
				return false
			}

			if !bytes.Equal(msg, ret) {
				t.Errorf("%v should equal %v", msg, ret)
				return false
			}
		}

		return true
	}

	if !fn([]byte{2, 3, 5, 7, 11}) {
		t.Errorf("should not be false")
	}
}

func TestBroadcastOthers(t *testing.T) {
	log.Println("TestBroadcastOthers")
	broadcast := NewTestServer()
	broadcast.m.HandleConnect(func(session *Session) {
		session.Register(map[string]interface{}{testChannel: testChannel})
	})
	broadcast.m.HandleMessage(func(session *Session, msg []byte) {
		broadcast.m.BroadcastOthers(session, msg, testChannel, websocket.TextMessage)
	})
	broadcast.m.Config.PongWait = time.Second
	broadcast.m.Config.PingPeriod = time.Second * 9 / 10
	server := httptest.NewServer(broadcast)
	defer server.Close()

	n := 10

	fn := func(msg string) bool {
		conn, _ := NewDialer(server.URL)
		defer conn.Close()

		listeners := make([]*websocket.Conn, n)
		for i := 0; i < n; i++ {
			listener, _ := NewDialer(server.URL)
			listeners[i] = listener
			defer listeners[i].Close()
		}

		conn.WriteMessage(websocket.TextMessage, []byte(msg))

		for i := 0; i < n; i++ {
			_, ret, err := listeners[i].ReadMessage()

			if err != nil {
				t.Error(err)
				return false
			}

			if msg != string(ret) {
				t.Errorf("%s should equal %s", msg, string(ret))
				return false
			}
		}

		return true
	}

	if !fn("test") {
		t.Errorf("should not be false")
	}
}

func TestBroadcastBinaryOthers(t *testing.T) {
	log.Println("TestBroadcastBinaryOthers")
	broadcast := NewTestServer()
	broadcast.m.HandleConnect(func(session *Session) {
		session.Register(map[string]interface{}{testChannel: testChannel})
	})
	broadcast.m.HandleMessageBinary(func(session *Session, msg []byte) {
		broadcast.m.BroadcastOthers(session, msg, testChannel, websocket.BinaryMessage)
	})
	broadcast.m.Config.PongWait = time.Second
	broadcast.m.Config.PingPeriod = time.Second * 9 / 10
	server := httptest.NewServer(broadcast)
	defer server.Close()

	n := 10

	fn := func(msg []byte) bool {
		conn, _ := NewDialer(server.URL)
		defer conn.Close()

		listeners := make([]*websocket.Conn, n)
		for i := 0; i < n; i++ {
			listener, _ := NewDialer(server.URL)
			listeners[i] = listener
			defer listeners[i].Close()
		}

		conn.WriteMessage(websocket.BinaryMessage, []byte(msg))

		for i := 0; i < n; i++ {
			messageType, ret, err := listeners[i].ReadMessage()

			if err != nil {
				t.Error(err)
				return false
			}

			if messageType != websocket.BinaryMessage {
				t.Errorf("message type should be BinaryMessage")
				return false
			}

			if !bytes.Equal(msg, ret) {
				t.Errorf("%v should equal %v", msg, ret)
				return false
			}
		}

		return true
	}

	if !fn([]byte{2, 3, 5, 7, 11}) {
		t.Errorf("should not be false")
	}
}

func TestPingPong(t *testing.T) {
	log.Println("TestPingPong")
	noecho := NewTestServer()
	noecho.m.KeepAlive = KeepAlivePong
	noecho.m.Config.PongWait = time.Second
	noecho.m.Config.PingPeriod = time.Second * 9 / 10
	server := httptest.NewServer(noecho)
	defer server.Close()

	conn, err := NewDialer(server.URL)
	conn.SetPingHandler(func(string) error {
		return nil
	})
	defer conn.Close()

	if err != nil {
		t.Error(err)
	}

	conn.WriteMessage(websocket.TextMessage, []byte("test"))

	_, _, err = conn.ReadMessage()

	if err == nil {
		t.Error("there should be an error")
	}
}

func TestStop(t *testing.T) {
	log.Println("TestStop")
	noecho := NewTestServer()
	server := httptest.NewServer(noecho)
	defer server.Close()

	conn, err := NewDialer(server.URL)
	defer conn.Close()

	if err != nil {
		t.Error(err)
	}

	noecho.m.Close()
}

func TestSmallMessageBuffer(t *testing.T) {
	log.Println("TestSmallMessageBuffer")
	echo := NewTestServerHandler(func(session *Session, msg []byte) {
		session.Write(msg)
	})
	echo.m.Config.MessageBufferSize = 0
	echo.m.HandleError(func(s *Session, err error) {
		if err == nil {
			t.Error("there should be a buffer full error here")
		}
	})
	server := httptest.NewServer(echo)
	defer server.Close()

	conn, err := NewDialer(server.URL)
	defer conn.Close()

	if err != nil {
		t.Error(err)
	}

	conn.WriteMessage(websocket.TextMessage, []byte("12345"))
}

func TestPong(t *testing.T) {
	log.Println("TestPong")
	echo := NewTestServerHandler(func(session *Session, msg []byte) {
		session.Write(msg)
	})
	echo.m.KeepAlive = KeepAlivePing
	echo.m.Config.PongWait = time.Second
	echo.m.Config.PingPeriod = time.Second * 9 / 10
	server := httptest.NewServer(echo)
	defer server.Close()

	conn, err := NewDialer(server.URL)
	defer conn.Close()

	if err != nil {
		t.Error(err)
	}

	var fired int32
	atomic.StoreInt32(&fired, 0)
	echo.m.HandlePong(func(s *Session) {
		atomic.StoreInt32(&fired, 1)
	})

	conn.WriteMessage(websocket.PongMessage, nil)

	time.Sleep(time.Millisecond)

	if atomic.LoadInt32(&fired) == 0 {
		t.Error("should have fired pong handler")
	}
}

func BenchmarkSessionWrite(b *testing.B) {
	log.Println("BenchmarkSessionWrite")
	echo := NewTestServerHandler(func(session *Session, msg []byte) {
		session.Write(msg)
	})
	server := httptest.NewServer(echo)
	conn, _ := NewDialer(server.URL)
	defer server.Close()
	defer conn.Close()

	for n := 0; n < b.N; n++ {
		conn.WriteMessage(websocket.TextMessage, []byte("test"))
		conn.ReadMessage()
	}
}

func BenchmarkBroadcast(b *testing.B) {
	log.Println("BenchmarkBroadcast")
	echo := NewTestServerHandler(func(session *Session, msg []byte) {
		session.Write(msg)
	})
	server := httptest.NewServer(echo)
	defer server.Close()

	conns := make([]*websocket.Conn, 0)

	num := 100

	for i := 0; i < num; i++ {
		conn, _ := NewDialer(server.URL)
		conns = append(conns, conn)
	}

	for n := 0; n < b.N; n++ {
		echo.m.Broadcast([]byte("test"), testChannel, websocket.BinaryMessage)

		for i := 0; i < num; i++ {
			conns[i].ReadMessage()
		}
	}

	for i := 0; i < num; i++ {
		conns[i].Close()
	}
}
