package supervisor

import "testing"

// Lines captured from aria2c 1.37.0 at log-level=notice.
func TestLogPortState(t *testing.T) {
	const (
		listening = "2026-11-08 23:30:34.170357 [NOTICE] [HttpListenCommand.cc:108] IPv4 RPC: listening on TCP port 46133\n"
		v6Only    = "2026-11-08 23:30:34.170407 [NOTICE] [HttpListenCommand.cc:108] IPv6 RPC: listening on TCP port 46133\n"
		failed    = "2026-11-07 18:39:37.104172 [ERROR] [HttpListenCommand.cc:112] IPv4 RPC: failed to bind TCP port 46133\n"
	)
	cases := []struct {
		log  string
		port int
		want portState
	}{
		{"", 46133, portPending},
		{listening, 46133, portOurs},
		{listening, 4613, portPending}, // a prefix of the port must not match
		{v6Only, 46133, portPending},
		{failed + v6Only, 46133, portStolen},
		{listening + failed, 46133, portStolen},
	}
	for i, c := range cases {
		if got := logPortState([]byte(c.log), c.port); got != c.want {
			t.Errorf("case %d: got %v, want %v", i, got, c.want)
		}
	}
}
