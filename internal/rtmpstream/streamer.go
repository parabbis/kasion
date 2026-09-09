package rtmpstream

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
)

type Streamer struct {
	rtmpURL    string
	width      int
	height     int
	fps        int
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	mu         sync.Mutex
	running    bool
	textFile   string
}

func New(rtmpURL string, width, height, fps int) *Streamer {
	return &Streamer{
		rtmpURL: rtmpURL,
		width:   width,
		height:  height,
		fps:     fps,
		textFile: "/tmp/kasion_current_text.txt",
	}
}

func (s *Streamer) Start(initialText string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("streamer already running")
	}

	if err := os.WriteFile(s.textFile, []byte(wrapText(initialText, 62, 14)), 0644); err != nil {
		return fmt.Errorf("write initial text: %w", err)
	}

	// Build ffmpeg drawtext filter that reads text from the reloadable file
	fontPath := findFont()
	filter := fmt.Sprintf(
		"color=c=black:s=%dx%d:d=86400:r=%d,"+
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
		"-sc_threshold", "0",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "44100",
		"-f", "flv",
		"-flvflags", "no_duration_filesize",
		s.rtmpURL,
	}

	s.cmd = exec.Command("ffmpeg", args...)
	s.cmd.Stderr = os.Stderr

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	s.running = true
	log.Printf("RTMP stream started to %s", s.rtmpURL)

	go func() {
		if err := s.cmd.Wait(); err != nil {
			log.Printf("ffmpeg exited: %v", err)
		}
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	return nil
}

func (s *Streamer) UpdateText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return fmt.Errorf("streamer not running")
	}

	if err := os.WriteFile(s.textFile, []byte(wrapText(text, 62, 14)), 0644); err != nil {
		return fmt.Errorf("write text file: %w", err)
	}

	return nil
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

func (s *Streamer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.cmd == nil {
		return nil
	}

	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		s.cmd.Process.Kill()
	}

	s.running = false
	log.Printf("RTMP stream stopped")
	return nil
}

func (s *Streamer) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
