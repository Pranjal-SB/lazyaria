package config

import "testing"

func TestWithLive(t *testing.T) {
	cur, next := defaults(), defaults()
	next.Limits.GlobalDown = "1M"
	next.Engine.Aria2cPath = "/opt/aria2c"
	next.Keys["preset"] = []string{"vim"}
	got := WithLive(cur, next)
	if got.Limits.GlobalDown != "1M" || got.Keys["preset"][0] != "vim" {
		t.Errorf("live keys not taken: down=%q preset=%v", got.Limits.GlobalDown, got.Keys["preset"])
	}
	if got.Engine.Aria2cPath != "aria2c" {
		t.Errorf("restart-only key changed: aria2c_path=%q", got.Engine.Aria2cPath)
	}
	if cur.Limits.GlobalDown != "0" || cur.Keys["preset"][0] != "default" {
		t.Error("WithLive changed its input")
	}
}
