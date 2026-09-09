package rtmpstream

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Streamer struct {
	rtmpURL  string
	width    int
	height   int
	fps      int
	textFile string

	mu          sync.Mutex
	cmd         *exec.Cmd
	currentText string
	procAlive   bool
	stopped     bool
}

func New(rtmpURL string, width, height, fps int) *Streamer {
	return &Streamer{
		rtmpURL:  rtmpURL,
		width:    width,
		height:   height,
		fps:      fps,
		textFile: "/tmp/kasion_current_text.txt",
	}
}

// Start launches the self-healing supervisor. It never blocks: if ffmpeg dies
// for any reason it is automatically restarted (with backoff) using the last
// known text. Returns an error only if the initial write/process launch fails.
func (s *Streamer) Start(initialText string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.stopped {
		s.stopped = false
	}
	s.currentText = initialText
	if err := s.writeTextLocked(initialText); err != nil {
		return err
	}

	go s.supervise()
	return nil
}

// supervise is the restart loop. It blocks for the lifetime of the streamer.
func (s *Streamer) supervise() {
	backoff := 3 * time.Second
	for {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()

		err := s.launch()
		if err != nil {
			log.Printf("streamer: failed to start ffmpeg: %v (retrying in %s)", err, backoff)
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = 3 * time.Second

		// Wait for ffmpeg to exit.
		s.mu.Lock()
		cmd := s.cmd
		s.mu.Unlock()

		if cmd == nil {
			return
		}
		err = cmd.Wait()
		log.Printf("streamer: ffmpeg exited: %v", err)

		s.mu.Lock()
		s.procAlive = false
		s.mu.Unlock()

		// Pause briefly before relaunching so a fast crash-loop can't
		// hammer the RTMP ingest.
		if backoff < 30*time.Second {
			backoff *= 2
		}
		time.Sleep(backoff)
	}
}

// launch starts a fresh ffmpeg process using the current text.
func (s *Streamer) launch() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fontPath := findFont()
	filter := fmt.Sprintf(
		"color=c=black:s=%dx%d:r=%d,"+
			"drawtext=fontfile=%s"+
			":textfile=%s"+
			":fontcolor=white:fontsize=48"+
			":line_spacing=28"+
			":x=(w-text_w)/2:y=(h-text_h)/2"+
			":reload=1",
		s.width, s.height, s.fps, fontPath, s.textFile,
	)

	args := []string{
		"-re",
		"-f", "lavfi", "-i", filter,
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo",
		"-c:v", "libopenh264",
		"-b:v", "2500k",
		"-maxrate", "3000k",
		"-bufsize", "1000k",
		"-g", "60",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "44100",
		"-f", "flv",
		"-flvflags", "no_duration_filesize",
		s.rtmpURL,
	}

	// Ensure the text file is present before ffmpeg starts.
	if err := s.writeTextLocked(s.currentText); err != nil {
		return err
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Stop() may have been called while we were between the stopped-check
	// and this start. If so, kill the process immediately rather than
	// leaving a stray ffmpeg running.
	if s.stopped {
		cmd.Process.Kill()
		s.procAlive = false
		return fmt.Errorf("start aborted: streamer stopped")
	}

	s.cmd = cmd
	s.procAlive = true
	log.Printf("streamer: ffmpeg started -> %s", s.rtmpURL)
	return nil
}

// UpdateText stores the text and atomically writes it to the reloaded text
// file. It works even if ffmpeg is momentarily down so the next restart
// resumes with the latest message.
func (s *Streamer) UpdateText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentText = text
	return s.writeTextLocked(text)
}

// writeTextLocked writes atomically (tmp + rename) so drawtext's reload=1
// never reads a partially written file. Callers must hold the mutex.
func (s *Streamer) writeTextLocked(text string) error {
	tmp := s.textFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(wrapText(text, 62, 14)), 0644); err != nil {
		return fmt.Errorf("write text tmp: %w", err)
	}
	if err := os.Rename(tmp, s.textFile); err != nil {
		return fmt.Errorf("rename text file: %w", err)
	}
	return nil
}

func (s *Streamer) Stop() error {
	s.mu.Lock()
	s.stopped = true
	var cmd *exec.Cmd
	if s.cmd != nil && s.procAlive {
		cmd = s.cmd
	}
	s.procAlive = false
	s.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			cmd.Process.Kill()
		}
	}
	log.Printf("RTMP stream stopped")
	return nil
}

// IsRunning reports whether an ffmpeg process is currently alive.
func (s *Streamer) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.procAlive
}

// findFont locates an available TrueType font on the system.
func findFont() string {
	paths := []string{
		"/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf",
		"/usr/share/fonts/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
		"/usr/share/fonts/liberation-sans-fonts/LiberationSans-Regular.ttf",
	}
	for _, fp := range paths {
		if _, err := os.Stat(fp); err == nil {
			return fp
		}
	}
	log.Printf("Warning: no TTF font found, drawtext will fail")
	return ""
}

// wrapText breaks text into lines of at most maxChars characters per line,
// keeping word boundaries, and caps the total number of lines. Returns the
// wrapped text joined with newlines so ffmpeg's drawtext renders it as
// multiple centered lines.
func wrapText(text string, maxChars, maxLines int) string {
	words := splitWords(text)
	lines := make([]string, 0)
	current := ""

	for _, w := range words {
		if current == "" {
			current = w
			continue
		}
		if len(current)+1+len(w) <= maxChars {
			current += " " + w
		} else {
			lines = append(lines, current)
			current = w
		}
	}
	if current != "" {
		lines = append(lines, current)
	}

	if len(lines) > maxLines {
		lines = lines[:maxLines]
		last := lines[len(lines)-1]
		if len(last)+1 <= maxChars {
			lines[len(lines)-1] = last + "…"
		}
	}

	return joinLines(lines)
}

// splitWords splits text on whitespace, preserving the order of words.
func splitWords(text string) []string {
	words := make([]string, 0)
	start := -1
	for i := 0; i <= len(text); i++ {
		end := i == len(text)
		if end || text[i] == ' ' || text[i] == '\n' || text[i] == '\t' {
			if start != -1 {
				words = append(words, text[start:i])
				start = -1
			}
		} else if start == -1 {
			start = i
		}
	}
	return words
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}