package config

import (
	"os"
	"strings"
)

type Config struct {
	AIEndpoint  string
	AIModel     string
	AIAuthToken string

	KickChannelSlug string
	KickAuthToken   string

	RTMPURL      string
	RTMPFullURL  string

	WebPort string

	SystemPrompt string
}

func Load() *Config {
	rtmpBase := os.Getenv("RTMP_URL")
	rtmpKey := os.Getenv("RTMP_STREAM_KEY")

	// Build full RTMP URL: base + "/" + stream key
	fullRTMP := rtmpBase
	if rtmpKey != "" {
		fullRTMP = strings.TrimRight(rtmpBase, "/") + "/" + rtmpKey
	}

	return &Config{
		AIEndpoint:  getenv("AI_ENDPOINT", "https://openrouter.ai/api/v1"),
		AIModel:     getenv("AI_MODEL", "anthropic/claude-3-haiku"),
		AIAuthToken: os.Getenv("AI_AUTH_TOKEN"),

		KickChannelSlug: os.Getenv("KICK_CHANNEL_SLUG"),
		KickAuthToken:   os.Getenv("KICK_AUTH_TOKEN"),

		RTMPURL:     rtmpBase,
		RTMPFullURL: fullRTMP,

		WebPort: getenv("WEB_PORT", "8080"),

		SystemPrompt: getenv("SYSTEM_PROMPT", "You are a helpful AI streaming assistant. Respond concisely."),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
