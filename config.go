package wsplice

import "time"

type Config struct {
	FrameSizeLimit int64

	WriteTimeout time.Duration
	ReadTimeout  time.Duration
	DialTimeout  time.Duration

	HostnameAllowlist []string
}
