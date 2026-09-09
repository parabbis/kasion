package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"kasion/internal/aichat"
	"kasion/internal/config"
	"kasion/internal/kickchat"
	"kasion/internal/rtmpstream"
	"kasion/internal/webagent"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("KASION - AI Stream Agent starting...")

	_ = godotenv.Load()

	cfg := config.Load()

	if cfg.RTMPFullURL == "" {
		log.Fatal("RTMP_URL is required")
	}
	if cfg.AIAuthToken == "" {
		log.Fatal("AI_AUTH_TOKEN is required")
	}

	aiClient := aichat.New(cfg.AIEndpoint, cfg.AIModel, cfg.AIAuthToken)
	streamer := rtmpstream.New(cfg.RTMPFullURL, 1920, 1080, 30)
	kickMon := kickchat.New(cfg.KickChannelSlug, cfg.KickAuthToken)
	webUI := webagent.New(cfg.WebPort)

	// Start web agent
	go func() {
		if err := webUI.Start(); err != nil {
			log.Fatalf("Web agent error: %v", err)
		}
	}()

	// Start kick chat monitor if configured
	if cfg.KickChannelSlug != "" {
		if err := kickMon.Start(); err != nil {
			log.Printf("Failed to start Kick monitor: %v", err)
		}
	}

	// Start the stream
	if err := streamer.Start("KASION AI STREAM\nWaiting for prompt..."); err != nil {
		log.Printf("Failed to start stream: %v", err)
	}

	log.Println("Agent ready. Send prompts via web UI")

	// Report live status to the web UI
	go func() {
		for {
			webUI.SetStatus(streamer.IsRunning(), kickMon.Connected(), cfg.AIModel)
			time.Sleep(5 * time.Second)
		}
	}()

	// Handle admin prompts
	go func() {
		for prompt := range webUI.PromptCh() {
			log.Printf("Processing admin prompt: %s", prompt)

			streamer.UpdateText("Thinking...")

			response, err := aiClient.Send(cfg.SystemPrompt, prompt)
			if err != nil {
				log.Printf("AI error: %v", err)
				streamer.UpdateText(fmt.Sprintf("AI Error: %v", err))
				continue
			}

			log.Printf("AI response: %s", response)
			webUI.AddHistory("ai", response)
			streamer.UpdateText(response)
		}
	}()

	// Handle kick chat messages
	go func() {
		for msg := range kickMon.Messages() {
			log.Printf("Kick chat [%s]: %s", msg.Username, msg.Content)

			streamer.UpdateText(fmt.Sprintf("%s: %s", msg.Username, msg.Content))

			prompt := fmt.Sprintf("A viewer named %s in the kick chat said: %q. Respond briefly.", msg.Username, msg.Content)
			response, err := aiClient.Send(cfg.SystemPrompt, prompt)
			if err != nil {
				log.Printf("AI error for kick: %v", err)
				continue
			}

			webUI.AddHistory("kick:"+msg.Username, msg.Content)
			webUI.AddHistory("ai", response)
			streamer.UpdateText(response)
		}
	}()

	// Handle kick chat send requests
	go func() {
		for req := range webUI.KickMsgCh() {
			log.Printf("Manual kick chat message: %s", req.Message)
			// TODO: Implement sending messages to Kick chat
			webUI.AddHistory("admin-sent", req.Message)
		}
	}()

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received signal %v, shutting down...", sig)

	streamer.Stop()
	kickMon.Stop()
	fmt.Println("Goodbye.")
}
