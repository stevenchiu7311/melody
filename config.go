package melody

import "time"

// UseRedisPool - USE REDIS POOL
var UseRedisPool = true

// DebugRedisPoolConn -
var DebugRedisPoolConn = 0

// RedisRcvConn -
var RedisRcvConn = 10

// Config melody configuration struct.
type Config struct {
	WriteWait         time.Duration // Milliseconds until write times out.
	PongWait          time.Duration // Timeout for waiting on pong.
	PingPeriod        time.Duration // Milliseconds between pings.
	MaxMessageSize    int64         // Maximum size in bytes of a message.
	MessageBufferSize int           // The max amount of messages that can be in a sessions buffer before it starts dropping them.
}

func newConfig() *Config {
	return &Config{
		WriteWait:         60 * time.Second,
		PongWait:          60 * time.Second,
		PingPeriod:        (60 * time.Second * 9) / 10,
		MaxMessageSize:    512,
		MessageBufferSize: 256,
	}
}

// StatsRedis -
type StatsRedis struct {
	MaxIdle        int
	MaxActive      int
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
}

func newStatsRedis() *StatsRedis {
	return &StatsRedis{
		MaxIdle:        5,
		MaxActive:      20,
		ConnectTimeout: 10 * time.Second,
		ReadTimeout:    0,
		WriteTimeout:   0,
	}
}
