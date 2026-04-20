package models

import "time"

type Server struct {
	Name   string      `json:"name"`
	URL    string      `json:"url"`
	Kind   string      `json:"kind,omitempty"`
	Auth   *Auth       `json:"auth,omitempty"`
	SSH    *SSHConfig  `json:"ssh,omitempty"`
	Driver DriverCfg   `json:"driver"`
	Health *HealthCfg  `json:"health,omitempty"`
	Tags   []string    `json:"tags,omitempty"`
	Status RuntimeStat `json:"status"`
}

type Auth struct {
	Type  string `json:"type"` // bearer|basic|header
	Token string `json:"token,omitempty"`
	User  string `json:"user,omitempty"`
	Pass  string `json:"pass,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type SSHConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	KeyFile   string `json:"key_file,omitempty"`
	KeyPass   string `json:"key_pass,omitempty"`
	Password  string `json:"password,omitempty"`
	HostKeyFP string `json:"host_key_fp,omitempty"`
}

type DriverCfg struct {
	Type   string            `json:"type"` // http_poll|tail_file|mock|none
	URL    string            `json:"url,omitempty"`
	Path   string            `json:"path,omitempty"`
	Every  string            `json:"every,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

type HealthCfg struct {
	Type  string `json:"type"` // http|tcp
	Path  string `json:"path,omitempty"`
	Every string `json:"every,omitempty"`
}

type RuntimeStat struct {
	State    string    `json:"state"` // online|degraded|offline|unknown
	Ping     int       `json:"ping"`
	LastSeen time.Time `json:"last_seen"`
	LastErr  string    `json:"last_err,omitempty"`
}

type Event struct {
	ID          int64             `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	Server      string            `json:"server"`
	Service     string            `json:"service"`
	Level       string            `json:"level"` // error|warn|info|debug
	Message     string            `json:"message"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	Count       int               `json:"count,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Trace       string            `json:"trace,omitempty"`
	Raw         string            `json:"raw,omitempty"`
}

type SavedRequest struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Group    string            `json:"group,omitempty"`
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Server   string            `json:"server,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Favorite bool              `json:"favorite,omitempty"`
}

type AlertRule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Server    string   `json:"server,omitempty"`
	Service   string   `json:"service,omitempty"`
	Level     string   `json:"level,omitempty"`
	Regex     string   `json:"regex,omitempty"`
	Threshold int      `json:"threshold"`
	Window    string   `json:"window"`
	Cooldown  string   `json:"cooldown,omitempty"`
	Channels  []string `json:"channels"`
}

type AlertChannel struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"` // slack|telegram|webhook
	URL     string            `json:"url"`
	Token   string            `json:"token,omitempty"`
	ChatID  string            `json:"chat_id,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type AlertFiring struct {
	RuleID    string    `json:"rule_id"`
	RuleName  string    `json:"rule_name"`
	At        time.Time `json:"at"`
	Count     int       `json:"count"`
	Sample    Event     `json:"sample"`
	Delivered []string  `json:"delivered"`
	Errors    []string  `json:"errors,omitempty"`
}

type EventQuery struct {
	Server  string    `json:"server,omitempty"`
	Service string    `json:"service,omitempty"`
	Level   string    `json:"level,omitempty"`
	Regex   string    `json:"regex,omitempty"`
	Since   time.Time `json:"since,omitempty"`
	Until   time.Time `json:"until,omitempty"`
	Limit   int       `json:"limit,omitempty"`
}

type DebugRequestLike struct {
	Server  string            `json:"server"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type CommandRequest struct {
	Server  string `json:"server"`
	Command string `json:"command"`
	Timeout string `json:"timeout,omitempty"`
}

type CommandResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Duration int64  `json:"duration_ms"`
	Error    string `json:"error,omitempty"`
}
