package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/fsinfo"
)

// WriteConf writes the option file aria2c reads via --conf-path. It holds the
// RPC secret, so it is created 0600 and swapped in with a rename.
func WriteConf(path string, s *config.Settings, p config.Paths, port int, secret string) error {
	b := &confBuilder{}
	b.base(s, p, port, secret)
	b.extra(s.Engine.Aria2ExtraArgs)
	if b.err != nil {
		return b.err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".aria2.conf-*")
	if err != nil {
		return fmt.Errorf("write aria2 config: %w (check the runtime dir or run `lazyaria doctor`)", err)
	}
	_, err = f.WriteString(b.out.String())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
		return fmt.Errorf("write aria2 config: %w (check the runtime dir or run `lazyaria doctor`)", err)
	}
	return nil
}

type confBuilder struct {
	out strings.Builder
	err error
}

func (b *confBuilder) add(k, v string) {
	if b.err != nil {
		return
	}
	// A line break would let a value smuggle in its own option line.
	if strings.ContainsAny(k+v, "\r\n") {
		b.err = fmt.Errorf("aria2 option %q contains a line break; remove it from the config", k)
		return
	}
	b.out.WriteString(k + "=" + v + "\n")
}

func (b *confBuilder) addIf(k, v string) {
	if v != "" {
		b.add(k, v)
	}
}

func (b *confBuilder) bool(k string, v bool) { b.add(k, strconv.FormatBool(v)) }
func (b *confBuilder) int(k string, v int)   { b.add(k, strconv.Itoa(v)) }

// size writes a plain byte count: aria2 only knows K and M suffixes.
func (b *confBuilder) size(k, v string) {
	n, err := config.ParseSize(v)
	if err != nil && b.err == nil {
		b.err = fmt.Errorf("aria2 option %s: %w", k, err)
	}
	b.add(k, strconv.FormatInt(n, 10))
}

// secs writes whole seconds, the unit aria2 uses for every interval.
func (b *confBuilder) secs(k, v string) {
	d, err := config.ParseDuration(v)
	if err != nil && b.err == nil {
		b.err = fmt.Errorf("aria2 option %s: %w", k, err)
	}
	b.int(k, int(d/time.Second))
}

func (b *confBuilder) base(s *config.Settings, p config.Paths, port int, secret string) {
	b.add("enable-rpc", "true")
	b.add("rpc-listen-all", "false")
	b.int("rpc-listen-port", port)
	b.add("rpc-secret", secret)
	b.add("daemon", "false")
	b.add("quiet", "true")
	b.add("log", filepath.Join(p.StateDir, "aria2.log"))
	b.add("log-level", "warn")
	b.add("continue", "true")
	b.secs("auto-save-interval", s.Engine.AutoSaveInterval)
	b.size("disk-cache", s.Engine.DiskCache)
	b.int("max-tries", s.Retry.MaxTries)
	b.secs("retry-wait", s.Retry.RetryWait)
	b.int("max-file-not-found", s.Retry.MaxFileNotFound)
	alloc := s.Downloads.FileAllocation
	if alloc == "auto" {
		alloc = fsinfo.Allocation(p.DownloadDir)
	}
	b.add("file-allocation", alloc)
	b.add("dir", p.DownloadDir)
	b.int("max-concurrent-downloads", s.Downloads.MaxConcurrent)
	b.int("split", s.Downloads.Split)
	b.int("max-connection-per-server", s.Downloads.ConnectionsPerServer)
	b.size("min-split-size", s.Downloads.MinSplitSize)
	b.add("auto-file-renaming", "true")
	b.add("allow-overwrite", "false")
	b.bool("check-certificate", s.Network.CheckCertificate)
	b.add("follow-torrent", "true")
	b.bool("bt-save-metadata", s.BT.SaveMetadata)
	b.add("seed-ratio", strconv.FormatFloat(s.BT.SeedRatio, 'f', -1, 64))
	// seed_minutes 0 means no seeding whatever the ratio, and seed-time=0 is
	// what makes aria2 stop; seed-ratio=0 alone means "no ratio limit".
	b.int("seed-time", s.BT.SeedMinutes)
	b.add("dht-file-path", p.DHT)

	b.addIf("user-agent", s.Downloads.UserAgent)
	b.size("lowest-speed-limit", s.Downloads.LowestSpeedLimit)
	b.secs("connect-timeout", s.Downloads.ConnectTimeout)
	b.secs("timeout", s.Downloads.Timeout)
	b.size("max-overall-download-limit", s.Limits.GlobalDown)
	b.size("max-overall-upload-limit", s.Limits.GlobalUp)
	b.addIf("all-proxy", s.Network.AllProxy)
	b.addIf("no-proxy", s.Network.NoProxy)
	b.addIf("ca-certificate", s.Network.CACertificate)
	b.addIf("interface", s.Network.Interface)
	b.bool("disable-ipv6", s.Network.DisableIPv6)
	b.add("listen-port", s.BT.ListenPort)
	b.add("dht-listen-port", s.BT.DHTListenPort)
	b.bool("enable-dht", s.BT.EnableDHT)
	b.bool("enable-peer-exchange", s.BT.EnablePeerExchange)
	b.int("bt-max-peers", s.BT.MaxPeers)
	b.addIf("bt-tracker", strings.Join(s.BT.ExtraTrackers, ","))
	b.bool("bt-require-crypto", s.BT.RequireCrypto)
}

// extra appends engine.aria2_extra_args last so they override the defaults.
// Both "--name=value" and "--name value" are accepted; a bare "--flag" is true.
func (b *confBuilder) extra(args []string) {
	for i := 0; i < len(args) && b.err == nil; i++ {
		name, ok := strings.CutPrefix(args[i], "--")
		k, v, hasValue := strings.Cut(name, "=")
		if !ok || k == "" {
			b.err = fmt.Errorf("engine.aria2_extra_args: %q must use the long form --name=value", args[i])
			return
		}
		if !hasValue {
			v = "true"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				v = args[i]
			}
		}
		// Config validation already rejects these; this is the last line of
		// defence for the loopback-only, secret-protected RPC endpoint.
		if lk := strings.ToLower(k); strings.HasPrefix(lk, "rpc") || lk == "enable-rpc" {
			continue
		}
		b.add(k, v)
	}
}
