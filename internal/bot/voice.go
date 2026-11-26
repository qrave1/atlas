package bot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Telegram voice messages are OGG/Opus. WAV works with transcription providers
// even when their transcription endpoint does not accept OGG.
func convertVoice(ctx context.Context, audio []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "atlas-voice-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	input, output := filepath.Join(dir, "voice.ogg"), filepath.Join(dir, "voice.wav")
	if err = os.WriteFile(input, audio, 0600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-i", input, "-vn", "-ar", "16000", "-ac", "1", "-t", "301", output)
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("voice conversion failed; check ffmpeg installation")
	}
	info, err := os.Stat(output)
	if err != nil {
		return nil, err
	}
	if info.Size() > 12<<20 {
		return nil, fmt.Errorf("converted voice too large")
	}
	return os.ReadFile(output)
}
