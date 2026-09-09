package webagent

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"
)

type Agent struct {
	port      string
	tmpl      *template.Template
	history   []ChatEntry
	mu        sync.RWMutex
	promptCh  chan string
	kickMsgCh chan KickChatRequest
	streaming bool
	kickConn  bool
	model     string
}

type Status struct {
	Streaming     bool   `json:"streaming"`
	KickConnected bool   `json:"kickConnected"`
	Model         string `json:"model"`
}

type ChatEntry struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type KickChatRequest struct {
	Message string
}

type apiResponse struct {
	OK      bool        `json:"ok"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func New(port string) *Agent {
	return &Agent{
		port:      port,
		history:   make([]ChatEntry, 0),
		promptCh:  make(chan string, 10),
		kickMsgCh: make(chan KickChatRequest, 10),
	}
}

func (a *Agent) PromptCh() <-chan string {
	return a.promptCh
}

func (a *Agent) KickMsgCh() <-chan KickChatRequest {
	return a.kickMsgCh
}

func (a *Agent) AddHistory(role, content string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = append(a.history, ChatEntry{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	})
}

func (a *Agent) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", a.handleDashboard)
	mux.HandleFunc("/api/prompt", a.handlePrompt)
	mux.HandleFunc("/api/history", a.handleHistory)
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/kick-reply", a.handleKickReply)
	mux.HandleFunc("/api/send-chat", a.handleSendChat)

	addr := "0.0.0.0:" + a.port
	log.Printf("Web agent listening on http://%s", addr)
	return http.ListenAndServe(addr, mux)
}

func (a *Agent) handleDashboard(w http.ResponseWriter, r *http.Request) {
	dashboardHTML := `<!DOCTYPE html>
<html>
<head>
    <title>KASION - AI Stream Agent</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: 'Segoe UI', Tahoma, sans-serif; background: #0a0a0a; color: #e0e0e0; min-height: 100vh; }
        .header { background: linear-gradient(135deg, #1a1a2e 0%, #16213e 100%); padding: 20px 30px; border-bottom: 2px solid #0f3460; }
        .header h1 { font-size: 24px; color: #e94560; }
        .header p { color: #888; margin-top: 4px; }
        .container { max-width: 1200px; margin: 0 auto; padding: 20px; display: grid; grid-template-columns: 1fr 1fr; gap: 20px; }
        .panel { background: #111; border: 1px solid #222; border-radius: 8px; padding: 20px; }
        .panel h2 { color: #e94560; margin-bottom: 15px; font-size: 18px; }
        textarea { width: 100%; height: 150px; background: #1a1a1a; border: 1px solid #333; color: #fff; padding: 12px; border-radius: 6px; font-size: 14px; resize: vertical; font-family: inherit; }
        textarea:focus { outline: none; border-color: #e94560; }
        button { background: #e94560; color: white; border: none; padding: 10px 24px; border-radius: 6px; cursor: pointer; font-size: 14px; font-weight: bold; margin-top: 10px; }
        button:hover { background: #c73650; }
        button.secondary { background: #333; }
        button.secondary:hover { background: #444; }
        .chat-box { height: 400px; overflow-y: auto; background: #0d0d0d; border: 1px solid #222; border-radius: 6px; padding: 12px; }
        .chat-msg { margin-bottom: 10px; padding: 8px 12px; border-radius: 6px; }
        .chat-msg.ai { background: #1a1a2e; border-left: 3px solid #e94560; }
        .chat-msg.user { background: #16213e; border-left: 3px solid #0f3460; }
        .chat-msg.kick { background: #1a2e1a; border-left: 3px solid #45e960; }
        .chat-msg .role { font-size: 11px; color: #888; text-transform: uppercase; margin-bottom: 4px; }
        .chat-msg .content { font-size: 14px; line-height: 1.5; }
        .status-bar { display: flex; gap: 20px; margin-top: 10px; font-size: 13px; color: #666; }
        .status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 4px; }
        .status-dot.on { background: #45e960; }
        .status-dot.off { background: #e94560; }
        .kick-chat { margin-top: 15px; }
        .kick-input { display: flex; gap: 8px; margin-top: 10px; }
        .kick-input input { flex: 1; background: #1a1a1a; border: 1px solid #333; color: #fff; padding: 10px; border-radius: 6px; font-size: 14px; }
        .kick-input input:focus { outline: none; border-color: #45e960; }
        @media (max-width: 768px) { .container { grid-template-columns: 1fr; } }
    </style>
</head>
<body>
    <div class="header">
        <h1>KASION</h1>
        <p>AI Stream Agent &mdash; Connect your AI to Kick</p>
        <div class="status-bar" id="status-bar"></div>
    </div>
    <div class="container">
        <div class="panel">
            <h2>Admin Prompt</h2>
            <textarea id="prompt-input" placeholder="Type a prompt for the AI..."></textarea>
            <button onclick="sendPrompt()">Send to AI</button>
            <button class="secondary" onclick="loadHistory()">Refresh Chat</button>
            <div class="kick-chat">
                <h2 style="margin-top:20px">Send to Kick Chat</h2>
                <div class="kick-input">
                    <input id="kick-msg" placeholder="Message to send..." onkeypress="if(event.key==='Enter')sendKickMsg()">
                    <button onclick="sendKickMsg()" style="background:#45e960;margin-top:0">Send</button>
                </div>
            </div>
        </div>
        <div class="panel">
            <h2>Chat History</h2>
            <div class="chat-box" id="chat-history"></div>
        </div>
    </div>
    <script>
        function sendPrompt() {
            const prompt = document.getElementById('prompt-input').value.trim();
            if (!prompt) return;
            fetch('/api/prompt', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({prompt: prompt})
            }).then(r => r.json()).then(d => {
                if (d.ok) {
                    document.getElementById('prompt-input').value = '';
                    setTimeout(loadHistory, 2000);
                } else {
                    alert('Error: ' + d.error);
                }
            });
        }
        function loadHistory() {
            fetch('/api/history').then(r => r.json()).then(d => {
                const box = document.getElementById('chat-history');
                box.innerHTML = '';
                if (d.data) {
                    d.data.forEach(e => {
                        const cls = e.role === 'ai' ? 'ai' : e.role === 'kick' ? 'kick' : 'user';
                        box.innerHTML += '<div class="chat-msg ' + cls + '"><div class="role">' + e.role + '</div><div class="content">' + escapeHtml(e.content) + '</div></div>';
                    });
                    box.scrollTop = box.scrollHeight;
                }
            });
        }
        function sendKickMsg() {
            const msg = document.getElementById('kick-msg').value.trim();
            if (!msg) return;
            fetch('/api/send-chat', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({message: msg})
            }).then(r => r.json()).then(d => {
                if (d.ok) document.getElementById('kick-msg').value = '';
            });
        }
        function updateStatus() {
            fetch('/api/status').then(r => r.json()).then(d => {
                if (d.data) {
                    const bar = document.getElementById('status-bar');
                    const streamDot = d.data.streaming ? 'on' : 'off';
                    const kickDot = d.data.kickConnected ? 'on' : 'off';
                    bar.innerHTML = '<span><span class="status-dot ' + streamDot + '"></span>Stream</span>' +
                        '<span><span class="status-dot ' + kickDot + '"></span>Kick</span>' +
                        '<span>Model: ' + (d.data.model || 'N/A') + '</span>';
                }
            });
        }
        function escapeHtml(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/\n/g,'<br>'); }
        setInterval(updateStatus, 5000);
        setInterval(loadHistory, 10000);
        updateStatus();
        loadHistory();
    </script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(dashboardHTML))
}

func (a *Agent) handlePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		a.jsonResp(w, apiResponse{Error: "POST only"}, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonResp(w, apiResponse{Error: "invalid body"}, http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		a.jsonResp(w, apiResponse{Error: "empty prompt"}, http.StatusBadRequest)
		return
	}

	a.AddHistory("admin", req.Prompt)

	select {
	case a.promptCh <- req.Prompt:
		a.jsonResp(w, apiResponse{OK: true}, http.StatusOK)
	default:
		a.jsonResp(w, apiResponse{Error: "prompt queue full"}, http.StatusTooManyRequests)
	}
}

func (a *Agent) handleHistory(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	a.jsonResp(w, apiResponse{OK: true, Data: a.history}, http.StatusOK)
}

func (a *Agent) handleStatus(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	a.jsonResp(w, apiResponse{
		OK: true,
		Data: Status{
			Streaming:     a.streaming,
			KickConnected: a.kickConn,
			Model:         a.model,
		},
	}, http.StatusOK)
}

func (a *Agent) handleKickReply(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		a.jsonResp(w, apiResponse{Error: "POST only"}, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonResp(w, apiResponse{Error: "invalid body"}, http.StatusBadRequest)
		return
	}

	a.AddHistory("kick:"+req.Username, req.Content)

	a.jsonResp(w, apiResponse{OK: true}, http.StatusOK)
}

func (a *Agent) handleSendChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		a.jsonResp(w, apiResponse{Error: "POST only"}, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonResp(w, apiResponse{Error: "invalid body"}, http.StatusBadRequest)
		return
	}

	select {
	case a.kickMsgCh <- KickChatRequest{Message: req.Message}:
		a.jsonResp(w, apiResponse{OK: true}, http.StatusOK)
	default:
		a.jsonResp(w, apiResponse{Error: "queue full"}, http.StatusTooManyRequests)
	}
}

func (a *Agent) jsonResp(w http.ResponseWriter, data interface{}, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func (a *Agent) SetStatus(streaming, kickConnected bool, model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streaming = streaming
	a.kickConn = kickConnected
	a.model = model
}
