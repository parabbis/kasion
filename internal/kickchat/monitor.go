package kickchat

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ChatMessage struct {
	Username string
	Content  string
	Time     time.Time
}

type Monitor struct {
	channelSlug string
	authToken   string
	conn        *websocket.Conn
	messages    chan ChatMessage
	mu          sync.Mutex
	running     bool
	stopCh      chan struct{}
	chatroomID  int64
}

const (
	pusherURL = "wss://ws-us2.pusher.com/app/32cbd69e4b950bf97679"
	pusherKey = "32cbd69e4b950bf97679"
)

func New(channelSlug, authToken string) *Monitor {
	return &Monitor{
		channelSlug: channelSlug,
		authToken:   authToken,
		messages:    make(chan ChatMessage, 100),
		stopCh:      make(chan struct{}),
	}
}

func (m *Monitor) Messages() <-chan ChatMessage {
	return m.messages
}

func (m *Monitor) Connected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running && m.conn != nil
}

func (m *Monitor) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("monitor already running")
	}

	go m.connect()

	return nil
}

// getChatroomID fetches the chatroom ID for the channel from the Kick API
func (m *Monitor) getChatroomID() (int64, error) {
	url := fmt.Sprintf("https://kick.com/api/v2/channels/%s", m.channelSlug)

	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var data struct {
		Chatroom struct {
			ID int64 `json:"id"`
		} `json:"chatroom"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, err
	}

	if data.Chatroom.ID == 0 {
		return 0, fmt.Errorf("no chatroom ID for channel %s", m.channelSlug)
	}

	return data.Chatroom.ID, nil
}

func (m *Monitor) connect() {
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		// Get the chatroom ID first (cache it after first success)
		if m.chatroomID == 0 {
			id, err := m.getChatroomID()
			if err != nil {
				log.Printf("Failed to get chatroom ID: %v, retrying in 5s...", err)
				time.Sleep(5 * time.Second)
				continue
			}
			m.chatroomID = id
		}

		wsURL := pusherURL + "?protocol=7&client=js&version=8.4.0&flash=false"

		log.Printf("Connecting to Kick chat via Pusher (chatroom %d)...", m.chatroomID)

		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			log.Printf("Pusher WebSocket error: %v, retrying in 5s...", err)
			time.Sleep(5 * time.Second)
			continue
		}

		m.mu.Lock()
		m.conn = c
		m.running = true
		m.mu.Unlock()

		log.Printf("Connected to Kick chat, subscribing...")
		m.subscribeToChannels()

		m.readLoop(c)

		m.mu.Lock()
		m.running = false
		m.mu.Unlock()

		log.Printf("Disconnected from Kick, reconnecting in 3s...")
		c.Close()
		time.Sleep(3 * time.Second)
	}
}

// subscribeToChannels subscribes to the Pusher channels needed for chat
func (m *Monitor) subscribeToChannels() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn == nil {
		return
	}

	channels := []string{
		fmt.Sprintf("chatroom_%d", m.chatroomID),
		fmt.Sprintf("chatrooms.%d.v2", m.chatroomID),
		fmt.Sprintf("channel_%d", m.chatroomID),
		fmt.Sprintf("chatrooms.%d", m.chatroomID),
		fmt.Sprintf("channel.%d", m.chatroomID),
	}

	for _, ch := range channels {
		sub := map[string]interface{}{
			"event": "pusher:subscribe",
			"data": map[string]string{
				"auth":    "",
				"channel": ch,
			},
		}
		msg, _ := json.Marshal(sub)
		m.conn.WriteMessage(websocket.TextMessage, msg)
	}

	log.Printf("Subscribed to Kick chat channels")
}

func (m *Monitor) readLoop(conn *websocket.Conn) {
	for {
		select {
		case <-m.stopCh:
			conn.Close()
			return
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Read error: %v", err)
			return
		}

		m.handleMessage(message)
	}
}

type pusherEvent struct {
	Event   string          `json:"event"`
	Data    json.RawMessage `json:"data"`
	Channel string          `json:"channel"`
}

func (m *Monitor) handleMessage(raw []byte) {
	var ev pusherEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return
	}

	switch {
	case ev.Event == "pusher:connection_established":
		log.Printf("Pusher connection established")
	case ev.Event == "pusher_internal:subscription_succeeded":
		log.Printf("Subscription succeeded for %s", ev.Channel)
	case ev.Event == "pusher:pong":
		if m.conn != nil {
			m.conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"pusher:ping","data":"{}"}`))
		}
	case ev.Event == "pusher:error":
		log.Printf("Pusher error: %s", string(ev.Data))
	case ev.Event == "App\\Events\\ChatMessageEvent" || ev.Event == "chatMessage" || ev.Event == "ChatMessage":
		m.handleChatMessage(ev.Data)
	default:
		// Ignore other events like channel_updated, etc.
	}
}

func (m *Monitor) handleChatMessage(data json.RawMessage) {
	// The data field is a JSON-encoded string in Pusher
	var dataStr string
	if err := json.Unmarshal(data, &dataStr); err == nil {
		data = json.RawMessage(dataStr)
	}

	var msg struct {
		Content  string `json:"content"`
		Sender   struct {
			Username string `json:"username"`
		} `json:"sender"`
	}

	if err := json.Unmarshal(data, &msg); err != nil {
		log.Printf("Failed to parse chat message: %v", err)
		return
	}

	if msg.Sender.Username != "" && msg.Content != "" {
		log.Printf("Kick chat [%s]: %s", msg.Sender.Username, msg.Content)
		m.messages <- ChatMessage{
			Username: msg.Sender.Username,
			Content:  msg.Content,
			Time:     time.Now(),
		}
	}
}

func (m *Monitor) Stop() {
	close(m.stopCh)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil {
		m.conn.Close()
	}
}