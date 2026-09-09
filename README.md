# KASION — AI Stream Agent

An AI agent that runs a live Kick stream. It renders AI-generated text as white-on-black video, pushes it to your Kick channel via RTMP, reads the channel chat, and lets the AI respond to viewers.

```
Admin prompt ──▶ AI (OpenRouter) ──▶ white text on black video ──▶ ffmpeg ──▶ RTMPS ──▶ Kick
Kick chat ──▶ WebSocket (Pusher) ──▶ AI response ──▶ back to stream ──▶ viewers
```

## Architecture

| Component | Path | Role |
|-----------|------|------|
| `main.go` | root | Orchestrator: wires everything together |
| `internal/config` | `config.go` | Loads settings from `.env` |
| `internal/aichat` | `client.go` | OpenAI-compatible chat client (OpenRouter, OpenAI, etc.) |
| `internal/rtmpstream` | `streamer.go` | ffmpeg pipeline: black background + word-wrapped text → H.264/FLV → RTMPS |
| `internal/kickchat` | `monitor.go` | Connects to Kick's Pusher WebSocket, parses chat messages |
| `internal/webagent` | `agent.go` | Web dashboard + admin HTTP API |

## Requirements

- **Go 1.21+**
- **ffmpeg** with:
  - `libopenh264` (H.264 encoder) — enabled in Fedora/RHEL `ffmpeg-free`
  - `libfreetype` (for `drawtext`)
  - `gnutls` or `openssl` (TLS for `rtmps://`)
- A **DejaVu** (or any TrueType) font at `/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf`
- **OpenRouter** (or any OpenAI-compatible) API key
- A **Kick** channel with streaming enabled (provides the RTMPS ingest URL + stream key)

### Installing on Fedora / distrobox

```bash
sudo dnf install golang ffmpeg dejavu-sans-fonts -y
```

## Setup

1. **Install dependencies:**

   ```bash
   sudo dnf install golang ffmpeg dejavu-sans-fonts -y
   ```

2. **Configure:**

   ```bash
   cp .env.example .env
   ```

   Edit `.env`:

   ```ini
   # OpenAI-compatible endpoint
   AI_ENDPOINT=https://openrouter.ai/api/v1
   AI_MODEL=openrouter/free
   AI_AUTH_TOKEN=sk-or-v1-...          # your OpenRouter key

   # Kick channel
   KICK_CHANNEL_SLUG=saturation3       # your channel slug (from kick.com/<slug>)

   # RTMPS ingest (from your Kick streaming settings dashboard)
   # Kick uses AWS IVS ingest: rtmps://<host>:443/app/<stream-key>
   RTMP_URL=rtmps://fa723fc1b171.global-contribute.live-video.net:443/app
   RTMP_STREAM_KEY=sk_us-west-2_...    # your stream key

   # Web dashboard
   WEB_PORT=8080

   # Persona for the AI
   SYSTEM_PROMPT=You are a helpful AI streaming assistant on Kick...
   ```

3. **Build:**

   ```bash
   go build -o kasion .
   ```

## Running

```bash
./run.sh
```

or directly:

```bash
go build -o kasion . && ./kasion
```

On startup you should see:

```
RTMP stream started to rtmps://fa723fc1b171.global-contribute.live-video.net:443/app/sk_...
Connected to Kick chat, subscribing...
Pusher connection established
Subscription succeeded for chatrooms.<id>.v2
```

If the channel page on kick.com shows your live stream, you're ready.

### Running in the background

```bash
setsid ./run.sh > /tmp/kasion.log 2>&1 &
```

Check status: `curl http://localhost:8080/api/status` → `{"streaming":true,"kickConnected":true}`

## Using the Web Dashboard

Open `http://<your-host>:8080`:

1. **Admin Prompt** — type a prompt, hit *Send to AI*. The AI response appears on the black-screen stream within a few seconds.
2. **Chat History** — shows prompts, AI responses, and live Kick chat messages.
3. **Send to Kick Chat** — type a manual message to post into your channel's chat.
4. **Status bar** — green dot = streaming / Kick connected; shows the active model.

### API

| Method | Endpoint | Body | Description |
|--------|----------|------|-------------|
| `POST` | `/api/prompt` | `{"prompt":"..."}` | Send a prompt to the AI; response is put on the stream |
| `GET` | `/api/history` | — | Chat/prompt history |
| `GET` | `/api/status` | — | `streaming`, `kickConnected`, `model` |
| `POST` | `/api/send-chat` | `{"message":"..."}` | Send a manual Kick chat message |

Example:

```bash
curl -X POST http://localhost:8080/api/prompt \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Introduce yourself to the stream."}'
```

## How it works

1. **Admin prompt** → sent to OpenRouter (or any compatible endpoint) → AI text reply.
2. **Video generation** — ffmpeg builds a `1920x1080 30fps` black screen via the `color` filter, overlays the AI text using `drawtext` with `reload=1` (reads `/tmp/kasion_current_text.txt` every frame), and word-wraps text client-side so nothing falls off screen.
3. **Streaming** — encoded with `libopenh264` (H.264 + AAC) and pushed over `rtmps://` (AWS IVS ingest) to your Kick channel.
4. **Chat reading** — connects to Kick's Pusher WebSocket (`wss://ws-us2.pusher.com/app/32cbd69e4b950bf97679`), subscribes to `chatrooms.<id>.v2`, and forwards every message to the AI for a reply which is then rendered on the stream.

## Configuration reference

| Variable | Default | Description |
|----------|---------|-------------|
| `AI_ENDPOINT` | `https://openrouter.ai/api/v1` | Base URL of an OpenAI-compatible API |
| `AI_MODEL` | `openrouter/free` | Model id (free router avoids costs) |
| `AI_AUTH_TOKEN` | — | `Bearer` token (OpenRouter/OpenAI key) |
| `KICK_CHANNEL_SLUG` | — | Channel slug on kick.com |
| `KICK_AUTH_TOKEN` | — | Optional Kick access token |
| `RTMP_URL` | — | RTMP(S) ingest base URL |
| `RTMP_STREAM_KEY` | — | Stream key appended to `RTMP_URL` |
| `WEB_PORT` | `8080` | Web dashboard port |
| `SYSTEM_PROMPT` | — | AI system/persona prompt |

## Switching AI providers

Any OpenAI-compatible API works. Examples:

```ini
# OpenAI
AI_ENDPOINT=https://api.openai.com/v1
AI_MODEL=gpt-4o-mini

# OpenAI-compatible local (Ollama)
AI_ENDPOINT=http://localhost:11434/v1
AI_MODEL=llama3
```

## Troubleshooting

| Symptom | Cause / Fix |
|---------|-------------|
| `Unknown encoder 'libx264'` | This build of ffmpeg has no `x264`. KASION uses `libopenh264`; install it: `sudo dnf install ffmpeg` (full) or use a build with `libopenh264` |
| `no TTF font found` | Install a font: `sudo dnf install dejavu-sans-fonts -y` |
| Stream connects but Kick shows nothing | Wrong ingest path. AWS IVS requires `rtmps://<host>:443/app/<key>` — not `/live/` |
| `live_stream: null` in API | Check the actual channel page; the v2 API field isn't always authoritative |
| Chat not reading | Ensure `KICK_CHANNEL_SLUG` matches the URL slug exactly |
| WebSocket to Pusher fails | Outdated endpoint; Kick uses `wss://ws-us2.pusher.com/app/32cbd69e4b950bf97679` |

## Security notes

- `.env` holds real secrets (API keys + stream key) and is git-ignored — never commit it.
- The web dashboard is **unauthenticated**. Run it behind a reverse proxy/VPN if exposed publicly.