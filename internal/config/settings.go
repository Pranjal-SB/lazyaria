package config

// Settings is the full configuration. Tags drive the schema: default is the
// built-in value (list values are comma separated), live marks keys that apply
// without a daemon restart, kind selects extra validation (size, duration,
// extra_args).
type Settings struct {
	Version     int                 `toml:"version" default:"1" live:"false" help:"Config file format version."`
	Downloads   DownloadsSettings   `toml:"downloads"`
	Limits      LimitsSettings      `toml:"limits"`
	Network     NetworkSettings     `toml:"network"`
	Retry       RetrySettings       `toml:"retry"`
	BT          BTSettings          `toml:"bt"`
	Daemon      DaemonSettings      `toml:"daemon"`
	Engine      EngineSettings      `toml:"engine"`
	Reliability ReliabilitySettings `toml:"reliability"`
	Remove      RemoveSettings      `toml:"remove"`
	Privacy     PrivacySettings     `toml:"privacy"`
	History     HistorySettings     `toml:"history"`
	UI          UISettings          `toml:"ui"`

	// Keys maps an action to its key list. The "preset" entry is a single
	// element list holding the validated preset name (default|vim).
	Keys map[string][]string `toml:"keys"`
	// Theme maps a role to an ANSI index 0-15, "#rrggbb", "none" or "reverse".
	Theme map[string]string `toml:"theme"`

	Log      LogSettings      `toml:"log"`
	Advanced AdvancedSettings `toml:"advanced"`
}

type DownloadsSettings struct {
	Dir                  string `toml:"dir" default:"" live:"false" help:"Download directory; empty uses the XDG download dir or ~/Downloads."`
	RememberLastDir      bool   `toml:"remember_last_dir" default:"true" live:"true" help:"Offer the last used directory for new jobs."`
	MaxConcurrent        int    `toml:"max_concurrent" default:"5" live:"true" min:"1" help:"Maximum downloads running at once."`
	ConnectionsPerServer int    `toml:"connections_per_server" default:"16" live:"true" min:"1" max:"16" help:"Connections per server (aria2 caps this at 16)."`
	Split                int    `toml:"split" default:"16" live:"true" min:"1" help:"Pieces a file is split into."`
	MinSplitSize         string `toml:"min_split_size" default:"4M" live:"true" kind:"size" help:"Do not split pieces smaller than this."`
	StartPaused          bool   `toml:"start_paused" default:"false" live:"true" help:"Add new jobs in the paused state."`
	OnExists             string `toml:"on_exists" default:"rename" live:"true" enum:"rename|overwrite|ask" help:"What to do when the target file exists."`
	FileAllocation       string `toml:"file_allocation" default:"auto" live:"false" enum:"auto|falloc|prealloc|trunc|none" help:"File allocation method; auto picks per filesystem."`
	UserAgent            string `toml:"user_agent" default:"" live:"true" help:"HTTP user agent; empty uses the aria2 default."`
	LowestSpeedLimit     string `toml:"lowest_speed_limit" default:"0" live:"true" kind:"size" help:"Abort connections slower than this (bytes/s); 0 disables."`
	ConnectTimeout       string `toml:"connect_timeout" default:"60s" live:"false" kind:"duration" help:"Connection timeout."`
	Timeout              string `toml:"timeout" default:"60s" live:"false" kind:"duration" help:"Transfer timeout."`
}

type LimitsSettings struct {
	GlobalDown string `toml:"global_down" default:"0" live:"true" kind:"size" help:"Global download limit (bytes/s); 0 is unlimited."`
	GlobalUp   string `toml:"global_up" default:"0" live:"true" kind:"size" help:"Global upload limit (bytes/s); 0 is unlimited."`
}

type NetworkSettings struct {
	AllProxy         string `toml:"all_proxy" default:"" live:"true" help:"HTTP(S) proxy URL; empty falls back to environment variables."`
	NoProxy          string `toml:"no_proxy" default:"" live:"true" help:"Comma separated hosts that bypass the proxy."`
	CheckCertificate bool   `toml:"check_certificate" default:"true" live:"false" help:"Verify TLS certificates; a warning is shown when off."`
	CACertificate    string `toml:"ca_certificate" default:"" live:"false" help:"Path to a custom CA bundle."`
	Interface        string `toml:"interface" default:"" live:"false" help:"Bind to this network interface or IP."`
	DisableIPv6      bool   `toml:"disable_ipv6" default:"false" live:"false" help:"Disable IPv6."`
}

type RetrySettings struct {
	MaxTries           int      `toml:"max_tries" default:"5" live:"false" min:"0" help:"aria2 tries per connection."`
	RetryWait          string   `toml:"retry_wait" default:"10s" live:"false" kind:"duration" help:"Wait between aria2 retries."`
	MaxFileNotFound    int      `toml:"max_file_not_found" default:"3" live:"false" min:"0" help:"Give up after this many file-not-found replies."`
	AutoRetry          []string `toml:"auto_retry" default:"429" live:"true" enum:"429|503|network|timeout" help:"Failure kinds retried automatically."`
	BackoffBase        string   `toml:"backoff_base" default:"30s" live:"true" kind:"duration" help:"First automatic retry delay."`
	BackoffFactor      float64  `toml:"backoff_factor" default:"2.0" live:"true" min:"1" help:"Multiplier applied to the delay on each retry."`
	BackoffMaxAttempts int      `toml:"backoff_max_attempts" default:"5" live:"true" min:"0" help:"Maximum automatic retries per job."`
}

type BTSettings struct {
	SeedRatio          float64  `toml:"seed_ratio" default:"0.0" live:"true" min:"0" help:"Seed until this share ratio; 0 means no ratio target."`
	SeedMinutes        int      `toml:"seed_minutes" default:"0" live:"true" min:"0" help:"Seed for this many minutes; 0 means do not seed."`
	ListenPort         string   `toml:"listen_port" default:"6881-6999" live:"false" help:"BitTorrent listen port or range."`
	DHTListenPort      string   `toml:"dht_listen_port" default:"6881-6999" live:"false" help:"DHT listen port or range."`
	EnableDHT          bool     `toml:"enable_dht" default:"true" live:"false" help:"Enable DHT."`
	EnablePeerExchange bool     `toml:"enable_peer_exchange" default:"true" live:"false" help:"Enable peer exchange."`
	MaxPeers           int      `toml:"max_peers" default:"55" live:"false" min:"0" help:"Maximum peers per torrent."`
	ExtraTrackers      []string `toml:"extra_trackers" default:"" live:"false" help:"Trackers appended to every torrent."`
	RequireCrypto      bool     `toml:"require_crypto" default:"false" live:"false" help:"Require encrypted peer connections."`
	SaveMetadata       bool     `toml:"save_metadata" default:"true" live:"false" help:"Keep the .torrent for magnets (needed to resume)."`
}

type DaemonSettings struct {
	Mode      string `toml:"mode" default:"on-demand" live:"false" enum:"on-demand|always" help:"Run the daemon on demand or keep it always running."`
	IdleExit  string `toml:"idle_exit" default:"30s" live:"true" kind:"duration" help:"Exit after this long with no clients and no work."`
	AutoStart bool   `toml:"auto_start" default:"true" live:"false" help:"Clients start the daemon when it is missing."`
}

type EngineSettings struct {
	Aria2cPath        string   `toml:"aria2c_path" default:"aria2c" live:"false" help:"Path or name of the aria2c binary."`
	AttachURL         string   `toml:"attach_url" default:"" live:"false" help:"Attach to an existing aria2 RPC endpoint instead of spawning one."`
	AttachSecretFile  string   `toml:"attach_secret_file" default:"" live:"false" help:"File holding the secret for the attached endpoint."`
	Aria2ExtraArgs    []string `toml:"aria2_extra_args" default:"" live:"false" kind:"extra_args" help:"Extra aria2c arguments; rpc-* options are rejected."`
	DiskCache         string   `toml:"disk_cache" default:"64M" live:"false" kind:"size" help:"aria2 disk cache size."`
	AutoSaveInterval  string   `toml:"auto_save_interval" default:"10s" live:"false" kind:"duration" help:"How often aria2 saves its session."`
	RestartMaxBackoff string   `toml:"restart_max_backoff" default:"30s" live:"false" kind:"duration" help:"Longest wait between aria2 restarts."`
	CrashLoopExits    int      `toml:"crash_loop_exits" default:"5" live:"false" min:"1" help:"Exits within the window that count as a crash loop."`
	CrashLoopWindow   string   `toml:"crash_loop_window" default:"3s" live:"false" kind:"duration" help:"Window used to detect a crash loop."`
}

type ReliabilitySettings struct {
	InhibitSleep      bool   `toml:"inhibit_sleep" default:"true" live:"true" help:"Keep the system awake while downloading."`
	InhibitMode       string `toml:"inhibit_mode" default:"block" live:"true" enum:"block|delay" help:"Sleep inhibit mode."`
	DiskPrecheck      bool   `toml:"disk_precheck" default:"true" live:"true" help:"Check free space before starting a job."`
	DiskMarginPercent int    `toml:"disk_margin_percent" default:"1" live:"true" min:"0" max:"100" help:"Extra free space required, in percent."`
	Duplicates        string `toml:"duplicates" default:"ask" live:"true" enum:"ask|allow|skip" help:"How to handle a URL that is already queued."`
	UnmountedGuard    bool   `toml:"unmounted_guard" default:"true" live:"true" help:"Refuse to write when the target mount is missing."`
}

type RemoveSettings struct {
	DefaultAction string `toml:"default_action" default:"ask" live:"true" enum:"ask|list-only|trash" help:"Default action when removing a job."`
	UndoSeconds   int    `toml:"undo_seconds" default:"10" live:"true" min:"0" help:"Seconds an undo stays available; 0 disables undo."`
	Confirm       bool   `toml:"confirm" default:"true" live:"true" help:"Ask before removing."`
}

type PrivacySettings struct {
	StampOriginXattr bool `toml:"stamp_origin_xattr" default:"true" live:"true" help:"Write user.xdg.origin.url on finished files."`
	RedactLogs       bool `toml:"redact_logs" default:"true" live:"true" help:"Strip query strings and credentials from logs."`
}

type HistorySettings struct {
	KeepDays              int  `toml:"keep_days" default:"0" live:"true" min:"0" help:"Days of history to keep; 0 keeps everything."`
	PurgeRemovedAfterDays int  `toml:"purge_removed_after_days" default:"30" live:"true" min:"0" help:"Days before removed jobs are purged."`
	ShowInList            bool `toml:"show_in_list" default:"true" live:"true" help:"Show finished jobs in the main list."`
	ListLimit             int  `toml:"list_limit" default:"200" live:"true" min:"1" help:"History page size."`
}

type UISettings struct {
	Mouse               string `toml:"mouse" default:"all" live:"true" enum:"all|cell|off" help:"Mouse reporting mode."`
	DoubleClickMS       int    `toml:"double_click_ms" default:"400" live:"true" min:"50" help:"Double click interval in milliseconds."`
	Glyphs              string `toml:"glyphs" default:"unicode" live:"true" enum:"unicode|ascii" help:"Glyph set."`
	Units               string `toml:"units" default:"iec" live:"true" enum:"iec|si" help:"Size units (1024 or 1000 based)."`
	SpeedUnit           string `toml:"speed_unit" default:"bytes" live:"true" enum:"bytes|bits" help:"Show speeds in bytes or bits."`
	TimeFormat          string `toml:"time_format" default:"relative" live:"true" enum:"relative|24h|12h|iso" help:"Timestamp format."`
	DetailsPosition     string `toml:"details_position" default:"auto" live:"true" enum:"auto|right|bottom|hidden" help:"Where the details pane goes."`
	DetailsWidthPercent int    `toml:"details_width_percent" default:"34" live:"true" min:"10" max:"90" help:"Details pane width in percent."`
	WideBreakpoint      int    `toml:"wide_breakpoint" default:"100" live:"true" min:"1" help:"Columns at which the wide layout starts."`
	Sort                string `toml:"sort" default:"smart" live:"true" enum:"smart|added|name|progress|speed|size" help:"Default list sort."`
	PasteDetection      bool   `toml:"paste_detection" default:"true" live:"true" help:"Offer to add URLs found in pasted text."`
	ToastSeconds        int    `toml:"toast_seconds" default:"4" live:"true" min:"1" help:"Seconds a toast stays visible."`
	SparklineSeconds    int    `toml:"sparkline_seconds" default:"60" live:"true" min:"1" help:"Seconds of speed history in sparklines."`
	WindowTitle         bool   `toml:"window_title" default:"true" live:"true" help:"Set the terminal window title."`
	TitleFormat         string `toml:"title_format" default:"lazyaria ↓ {down} · {active} active" live:"true" help:"Window title template."`
	OSCLinks            bool   `toml:"osc_links" default:"true" live:"true" help:"Use OSC 8 hyperlinks."`
	Clipboard           string `toml:"clipboard" default:"auto" live:"true" enum:"auto|osc52|wl-copy|xclip|off" help:"Clipboard backend."`
	OpenCommand         string `toml:"open_command" default:"xdg-open" live:"true" help:"Command used to open files."`
	OpenFolderCommand   string `toml:"open_folder_command" default:"" live:"true" help:"Command used to open folders; empty uses open_command."`
	ReducedMotion       bool   `toml:"reduced_motion" default:"false" live:"true" help:"Turn off animation."`
	FirstRun            bool   `toml:"first_run" default:"true" live:"false" help:"Show the first-run screen; cleared after it is shown."`
}

type LogSettings struct {
	Level   string `toml:"level" default:"info" live:"true" enum:"debug|info|warn|error" help:"Log level."`
	File    string `toml:"file" default:"" live:"false" help:"Log file path; empty uses the state dir."`
	MaxSize string `toml:"max_size" default:"5M" live:"false" kind:"size" help:"Rotate the log at this size."`
	Keep    int    `toml:"keep" default:"3" live:"false" min:"0" help:"Rotated log files to keep."`
}

type AdvancedSettings struct {
	ProgressHz       int    `toml:"progress_hz" default:"4" live:"true" min:"1" help:"Maximum progress pushes per second per client."`
	PollInterval     string `toml:"poll_interval" default:"1s" live:"true" kind:"duration" help:"aria2 poll interval while observed."`
	SentinelInterval string `toml:"sentinel_interval" default:"15s" live:"true" kind:"duration" help:"aria2 poll interval while unobserved."`
	SocketPath       string `toml:"socket_path" default:"" live:"false" help:"Override the socket location."`
}
